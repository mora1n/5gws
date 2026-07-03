package checksum

import "testing"

func TestParseSHA256TextAcceptsBareHash(t *testing.T) {
	hash := "fc9fef3687e66108351b1e5e7d54a7df0ea394fde1ee7f20127d71fcbafe9e37"
	got, err := ParseSHA256Text(hash+"\n", "asset.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Fatalf("hash = %q, want %q", got, hash)
	}
}

func TestParseSHA256TextAcceptsFormattedChecksum(t *testing.T) {
	hash := "FC9FEF3687E66108351B1E5E7D54A7DF0EA394FDE1EE7F20127D71FCBAFE9E37"
	got, err := ParseSHA256Text(hash+"  asset.tar.gz\n", "asset.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fc9fef3687e66108351b1e5e7d54a7df0ea394fde1ee7f20127d71fcbafe9e37" {
		t.Fatalf("hash = %q", got)
	}
}

func TestParseSHA256TextRejectsFilenameMismatch(t *testing.T) {
	hash := "fc9fef3687e66108351b1e5e7d54a7df0ea394fde1ee7f20127d71fcbafe9e37"
	if _, err := ParseSHA256Text(hash+"  other.tar.gz\n", "asset.tar.gz"); err == nil {
		t.Fatal("expected filename mismatch")
	}
}

func TestParseSHA256TextRejectsInvalidHash(t *testing.T) {
	if _, err := ParseSHA256Text("not-a-hash  asset.tar.gz\n", "asset.tar.gz"); err == nil {
		t.Fatal("expected invalid hash")
	}
}
