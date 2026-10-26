package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"golang.org/x/net/netutil"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
	"uniclog.io/sonoryx/internal/filetransfer"
	"uniclog.io/sonoryx/internal/media"
	"uniclog.io/sonoryx/internal/persist"
	"uniclog.io/sonoryx/internal/voice"
)

type fileServerConfig struct {
	Port         int
	Identity     string
	Storage      filetransfer.Config
	ReadyFile    string
	ActivateFile string
	Activation   <-chan struct{}
}

func startFileServer(ctx context.Context, cancel context.CancelFunc, store *persist.Store, hub *voice.Hub, c fileServerConfig, gates ...*sync.Mutex) (func() error, error) {
	if c.Port == 0 {
		return func() error { return nil }, nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, errors.New("file-port must be 0..65535")
	}
	identity, err := media.LoadOrCreateIdentity(c.Identity+".crt", c.Identity+".key")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", c.Port))
	if err != nil {
		return nil, err
	}
	handler, err := filetransfer.New(store, hub, c.Storage, gates...)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	listener = netutil.LimitListener(listener, 128)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, BaseContext: func(net.Listener) context.Context { return ctx }}
	hub.SetFileEndpoint(uint16(c.Port), identity.Fingerprint)
	log.Printf("files configured: port=%d directory=%q max_file_bytes=%d fingerprint=%s", c.Port, c.Storage.Directory, c.Storage.MaxFileBytes, identity.Fingerprint)
	done := make(chan error, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if c.Activation != nil {
			select {
			case <-ctx.Done():
				return
			case <-c.Activation:
			}
		}
		handler.Run(ctx)
	}()
	go func() {
		err := server.ServeTLS(listener, identity.CertPath, identity.KeyPath)
		done <- err
		if !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}()
	var once sync.Once
	var result error
	return func() error {
		once.Do(func() {
			hub.SetFileEndpoint(0, "")
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
			}
			err := <-done
			if !errors.Is(err, http.ErrServerClosed) {
				result = err
			}
			<-workerDone
		})
		return result
	}, nil
}
