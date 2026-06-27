package ssrust

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/morain/5gws/internal/config"
)

type localConfig struct {
	Server       string `json:"server"`
	ServerPort   int    `json:"server_port"`
	LocalAddress string `json:"local_address"`
	LocalPort    int    `json:"local_port"`
	Password     string `json:"password"`
	Timeout      int    `json:"timeout,omitempty"`
	Method       string `json:"method"`
	Mode         string `json:"mode"`
}

func Config(exit config.ExitConfig) (string, error) {
	if exit.Type != "shadowsocks-rust" {
		return "", fmt.Errorf("exit %q is %q, not shadowsocks-rust", exit.Name, exit.Type)
	}
	timeout := exit.TimeoutSeconds
	if timeout == 0 {
		timeout = 300
	}
	cfg := localConfig{
		Server:       exit.Server,
		ServerPort:   exit.ServerPort,
		LocalAddress: exit.ListenAddress,
		LocalPort:    exit.ListenPort,
		Password:     exit.Password,
		Timeout:      timeout,
		Method:       exit.Method,
		Mode:         exit.SSRustMode(),
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func LocalAddr(exit config.ExitConfig) string {
	return net.JoinHostPort(exit.ListenAddress, fmt.Sprint(exit.ListenPort))
}

func ServiceName(exit config.ExitConfig) string {
	return "5gws-ssrust-" + exit.Name + ".service"
}
