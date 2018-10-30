package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Bilibili/discovery/naming"
	"github.com/Terry-Mao/goim/internal/comet"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	"github.com/Terry-Mao/goim/internal/comet/grpc"
	xgrpc "github.com/Terry-Mao/goim/pkg/grpc"
	"github.com/Terry-Mao/goim/pkg/ip"
	log "github.com/golang/glog"
)

const (
	ver = "2.0.0"
)

func main() {
	flag.Parse()
	if err := conf.Init(); err != nil {
		panic(err)
	}
	runtime.GOMAXPROCS(conf.Conf.MaxProc)
	rand.Seed(time.Now().UTC().UnixNano())
	log.Infof("goim-comet [version: %s] start", ver)
	// grpc register naming
	dis := naming.New(conf.Conf.Naming)
	xgrpc.Register(dis)
	// server
	srv := comet.NewServer(conf.Conf)
	if err := comet.InitWhitelist(conf.Conf.Whitelist); err != nil {
		panic(err)
	}
	if err := comet.InitTCP(srv, conf.Conf.TCP.Bind, conf.Conf.MaxProc); err != nil {
		panic(err)
	}
	if err := comet.InitWebsocket(srv, conf.Conf.WebSocket.Bind, conf.Conf.MaxProc); err != nil {
		panic(err)
	}
	if conf.Conf.WebSocket.TLSOpen {
		if err := comet.InitWebsocketWithTLS(srv, conf.Conf.WebSocket.TLSBind, conf.Conf.WebSocket.CertFile, conf.Conf.WebSocket.PrivateFile, runtime.NumCPU()); err != nil {
			panic(err)
		}
	}
	// grpc
	rpcSrv := grpc.New(conf.Conf.RPCServer, srv)
	env := conf.Conf.Env
	addr := ip.InternalIP()
	_, port, _ := net.SplitHostPort(conf.Conf.RPCServer.Addr)
	ins := &naming.Instance{
		Region:   env.Region,
		Zone:     env.Zone,
		Env:      env.DeployEnv,
		Hostname: env.Hostname,
		AppID:    "goim.comet",
		Addrs: []string{
			"grpc://" + addr + ":" + port,
		},
		Metadata: map[string]string{
			"weight":   env.Weight,
			"offline":  env.Offline,
			"ip_addrs": strings.Join(env.IPAddrs, ","),
		},
	}
	disCancel, err := dis.Register(ins)
	if err != nil {
		panic(err)
	}
	// renew discovery metadata
	go func() {
		for {
			var (
				err   error
				conns int
				ips   = make(map[string]struct{})
			)
			for _, bucket := range srv.Buckets() {
				for ip := range bucket.IPCount() {
					ips[ip] = struct{}{}
				}
				conns += bucket.ChannelCount()
			}
			ins.Metadata["conns"] = fmt.Sprint(conns)
			ins.Metadata["ips"] = fmt.Sprint(len(ips))
			//if err = dis.Set(ins); err != nil {
			log.Errorf("dis.Set(%+v) error(%v)", ins, err)
			//	time.Sleep(time.Second)
			//	continue
			//}
			time.Sleep(time.Duration(conf.Conf.ServerTick))
		}
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		log.Infof("goim-comet get a signal %s", s.String())
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			log.Infof("goim-comet [version: %s] exit", ver)
			if disCancel != nil {
				disCancel()
			}
			srv.Close()
			rpcSrv.GracefulStop()
			return
		case syscall.SIGHUP:
		default:
			return
		}
	}
}
