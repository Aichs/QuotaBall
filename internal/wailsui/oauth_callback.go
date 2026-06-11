package wailsui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const localOAuthRedirectURI = "http://127.0.0.1:27182/oauth/linuxdo"

var startOAuthCallback = startLocalOAuthCallback

type oauthCapture struct {
	RedirectURI string
	Callbacks   <-chan string
	Done        <-chan struct{}
	close       func()
}

func (c *oauthCapture) Close() {
	if c != nil && c.close != nil {
		c.close()
	}
}

func startLocalOAuthCallback(ctx context.Context) (*oauthCapture, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:27182")
	if err != nil {
		return nil, errors.New("自动回调端口 27182 被占用，请关闭其他 QuotaBall 窗口后重试")
	}

	callbacks := make(chan string, 1)
	done := make(chan struct{})
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	server.Handler = mux
	mux.HandleFunc("/oauth/linuxdo", func(w http.ResponseWriter, r *http.Request) {
		callbackURL := "http://127.0.0.1:27182" + r.URL.RequestURI()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<!doctype html><meta charset=\"utf-8\"><title>QuotaBall</title><body>登录已完成，可以返回 QuotaBall。</body>")
		select {
		case callbacks <- callbackURL:
		case <-done:
		default:
		}
	})

	var once sync.Once
	stop := func() {
		once.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			_ = listener.Close()
			close(done)
		})
	}
	go func() {
		_ = server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		stop()
	}()

	return &oauthCapture{
		RedirectURI: localOAuthRedirectURI,
		Callbacks:   callbacks,
		Done:        done,
		close:       stop,
	}, nil
}
