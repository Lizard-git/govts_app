package clientapp

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestMediaEndpointErrorExplainsServerConfiguration(t *testing.T) {
	err := mediaEndpointError(netip.MustParseAddrPort("193.187.92.89:9002"), &net.DNSError{IsTimeout: true})
	message := err.Error()
	for _, want := range []string{"неверная конфигурация сервера", "193.187.92.89:9002", "TCP-порт", "firewall"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
}

func TestMediaEndpointErrorPreservesUnexpectedCause(t *testing.T) {
	cause := errors.New("unexpected TLS failure")
	err := mediaEndpointError(netip.MustParseAddrPort("127.0.0.1:9002"), cause)
	if !errors.Is(err, cause) {
		t.Fatalf("error %v does not preserve cause", err)
	}
}
