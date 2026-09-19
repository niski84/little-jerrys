// A separately provisioned root-owned loopback service, never started by the app.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/niski84/little-jerrys/internal/maintainer"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9137", "loopback listen address; use a trusted tunnel")
	target := flag.String("target", "", "registered target ID")
	device := flag.String("device-id", "", "stable app health device ID")
	stage := flag.String("stage-binary", "", "operator-only: stage an already reviewed local arm64 binary, then exit")
	revision := flag.String("revision", "", "exact clean commit for staged artifact")
	flag.Parse()
	if os.Geteuid() != 0 {
		log.Fatal("executor/stager must be provisioned as root")
	}
	const root = "/var/lib/jerry-maintainer"
	if *stage != "" {
		if err := maintainer.StageArtifact(root, *stage, *revision); err != nil {
			log.Fatal(err)
		}
		log.Print("Exact artifact staged; nothing installed or restarted")
		return
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("listen must be a numeric loopback address")
	}
	executor, err := maintainer.New(maintainer.Options{Root: root, Binary: "/opt/jerry/jerry", HealthURL: "http://127.0.0.1:8089/api/health", Target: *target, DeviceID: *device, Token: os.Getenv("JERRY_MAINTAINER_TOKEN"), Restart: func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "restart", "jerry.service")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
		return cmd.Run()
	}})
	if err != nil {
		log.Fatal(err)
	}
	defer executor.Close()
	server := http.Server{Addr: *listen, Handler: executor, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = server.Shutdown(stop)
	}()
	log.Print("Maintainer executor listening on loopback; exact staged artifacts only")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Print(err)
	}
}
