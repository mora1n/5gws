package quic

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

const (
	quicVersion1      = 0x00000001
	longHeaderForm    = 0x80
	initialPacketType = 0x00
	initialSaltV1     = "\x38\x76\x2c\xf7\xf5\x59\x34\xb3\x4d\x17\x9a\xe6\xa4\xc8\x0c\xad\xcc\xbb\x7f\x0a"
	tagSize           = 16
)

func ExtractSNI(data []byte) (string, bool) {
	if len(data) < 5 || data[0]&longHeaderForm == 0 {
		return "", false
	}
	if binary.BigEndian.Uint32(data[1:5]) != quicVersion1 {
		return "", false
	}
	if (data[0]&0x30)>>4 != initialPacketType {
		return "", false
	}
	off := 5
	dcid, next, ok := readConnectionID(data, off)
	if !ok {
		return "", false
	}
	off = next
	_, off, ok = readConnectionID(data, off)
	if !ok {
		return "", false
	}
	tokenLen, n := readVarint(data[off:])
	if n == 0 || len(data) < off+n+int(tokenLen) {
		return "", false
	}
	off += n + int(tokenLen)
	length, n := readVarint(data[off:])
	if n == 0 || uint64(len(data)-off) < length {
		return "", false
	}
	off += n
	if off+int(length) > len(data) {
		return "", false
	}
	return decryptInitial(data, off, int(length), dcid)
}

func readConnectionID(data []byte, off int) ([]byte, int, bool) {
	if len(data) < off+1 {
		return nil, off, false
	}
	size := int(data[off])
	off++
	if len(data) < off+size {
		return nil, off, false
	}
	return data[off : off+size], off + size, true
}

func decryptInitial(data []byte, off, length int, dcid []byte) (string, bool) {
	protected := data[off : off+length]
	initialSecret := hkdfExtract([]byte(initialSaltV1), dcid)
	clientSecret := hkdfExpandLabel(initialSecret, "client in", nil, 32)
	key := hkdfExpandLabel(clientSecret, "quic key", nil, 16)
	iv := hkdfExpandLabel(clientSecret, "quic iv", nil, 12)
	hp := hkdfExpandLabel(clientSecret, "quic hp", nil, 16)
	hpCipher, err := aes.NewCipher(hp)
	if err != nil || len(protected) < 20 {
		return "", false
	}
	sample := protected[4:20]
	mask := make([]byte, 16)
	hpCipher.Encrypt(mask, sample)
	firstByte := data[0] ^ (mask[0] & 0x1f)
	pnLen := int(firstByte&0x03) + 1
	if len(protected) < pnLen {
		return "", false
	}
	packetNumber := make([]byte, pnLen)
	for i := 0; i < pnLen; i++ {
		packetNumber[i] = protected[i] ^ mask[1+i]
	}
	nonce := append([]byte(nil), iv...)
	for i := 0; i < pnLen; i++ {
		nonce[11-i] ^= packetNumber[pnLen-1-i]
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", false
	}
	aad := make([]byte, off+pnLen)
	copy(aad, data[:off+pnLen])
	aad[0] = firstByte
	copy(aad[off:], packetNumber)
	ciphertext := protected[pnLen:]
	if len(ciphertext) < tagSize {
		return "", false
	}
	plain, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", false
	}
	return parseCryptoFrames(plain)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func parseCryptoFrames(plaintext []byte) (string, bool) {
	off := 0
	for off < len(plaintext) {
		frameType := plaintext[off]
		off++
		switch frameType {
		case 0x00:
			for off < len(plaintext) && plaintext[off] == 0x00 {
				off++
			}
		case 0x06:
			_, n := readVarint(plaintext[off:])
			if n == 0 {
				return "", false
			}
			off += n
			dataLen, n := readVarint(plaintext[off:])
			if n == 0 || uint64(len(plaintext)-off) < dataLen {
				return "", false
			}
			off += n
			if sni, ok := ExtractSNIFromClientHello(plaintext[off : off+int(dataLen)]); ok {
				return sni, true
			}
			off += int(dataLen)
		default:
			return "", false
		}
	}
	return "", false
}

func ExtractSNIFromClientHello(data []byte) (string, bool) {
	if len(data) < 5 {
		return "", false
	}
	hs := data
	if data[0] == 0x16 {
		recordLen := binary.BigEndian.Uint16(data[3:5])
		if len(data) < 5+int(recordLen) {
			return "", false
		}
		hs = data[5 : 5+int(recordLen)]
	}
	if len(hs) < 4 || hs[0] != 0x01 {
		return "", false
	}
	hello := hs[4:]
	if len(hello) < 34 {
		return "", false
	}
	off := 34
	sessionIDLen := int(hello[off])
	off++
	if len(hello) < off+sessionIDLen+2 {
		return "", false
	}
	off += sessionIDLen
	csLen := int(binary.BigEndian.Uint16(hello[off : off+2]))
	off += 2
	if len(hello) < off+csLen+1 {
		return "", false
	}
	off += csLen
	cmLen := int(hello[off])
	off++
	if len(hello) < off+cmLen+2 {
		return "", false
	}
	off += cmLen
	extLen := int(binary.BigEndian.Uint16(hello[off : off+2]))
	off += 2
	if len(hello) < off+extLen {
		return "", false
	}
	return extractSNIExtension(hello[off : off+extLen])
}

func extractSNIExtension(extensions []byte) (string, bool) {
	for off := 0; off+4 <= len(extensions); {
		extType := binary.BigEndian.Uint16(extensions[off : off+2])
		size := int(binary.BigEndian.Uint16(extensions[off+2 : off+4]))
		off += 4
		if off+size > len(extensions) {
			return "", false
		}
		if extType == 0 {
			return parseServerNameList(extensions[off : off+size])
		}
		off += size
	}
	return "", false
}

func parseServerNameList(data []byte) (string, bool) {
	if len(data) < 5 {
		return "", false
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen || data[2] != 0 {
		return "", false
	}
	nameLen := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+nameLen {
		return "", false
	}
	return string(data[5 : 5+nameLen]), true
}

func readVarint(buf []byte) (uint64, int) {
	if len(buf) == 0 {
		return 0, 0
	}
	first := buf[0]
	length := 1 << (first >> 6)
	if len(buf) < length {
		return 0, 0
	}
	switch length {
	case 1:
		return uint64(first & 0x3f), 1
	case 2:
		return uint64(first&0x3f)<<8 | uint64(buf[1]), 2
	case 4:
		return uint64(first&0x3f)<<24 | uint64(buf[1])<<16 | uint64(buf[2])<<8 | uint64(buf[3]), 4
	default:
		return binary.BigEndian.Uint64(buf) & 0x3fffffffffffffff, 8
	}
}

func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	var out, prev []byte
	for i := byte(1); len(out) < length; i++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{i})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:length]
}

func hkdfExpandLabel(secret []byte, label string, context []byte, length int) []byte {
	full := "tls13 " + label
	info := []byte{byte(length >> 8), byte(length), byte(len(full))}
	info = append(info, full...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return hkdfExpand(secret, info, length)
}
