package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/patsak/stun"
)

const (
	serverFlag = "server"
)

var (
	tunN              int
	clientPort        int
	networkCIDR       string
	peerEndpoint      string
	forceRouteDomains string
	verbose           bool
	server            bool
	dnsServer         string
)

func init() {
	flag.BoolVar(&server, serverFlag, false, "server mode")
	flag.BoolVar(&verbose, "v", false, "verbose output")
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.IntVar(&tunN, "tun-number", 5, "tunnel device id")
	flag.IntVar(&clientPort, "client-port", 1200, "client port")
	flag.IntVar(&clientPort, "cp", 1200, "client port")
	flag.StringVar(&networkCIDR, "n", "192.168.50.1/24", "vpn network")
	flag.StringVar(&networkCIDR, "network-cidr", "192.168.50.1/24", "vpn network")
	flag.StringVar(&peerEndpoint, "p", ":1300", "public peer in format ip:port")
	flag.StringVar(&peerEndpoint, "peer-endpoint", ":1300", "public peer in format ip:port")
	flag.StringVar(&forceRouteDomains, "f", "", "file with domains to force redirecting traffic via tunnel")
	flag.StringVar(&forceRouteDomains, "force-route-domains", "", "file with domains to force redirecting traffic via tunnel")
	flag.StringVar(&dnsServer, "dns-server", "8.8.8.8", "dns server")
}

func main() {
	flag.Parse()

	if verbose {
		log.Default().SetLevel(log.DebugLevel)
	}

	tun, err := stun.InitTunDevice(tunN)
	if err != nil {
		panic(err)
	}
	defer tun.Close()

	serverIP, serverPort, err := parseHostAndPort(peerEndpoint)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if server {
		cfg := stun.ServerConfig{
			ServerPort:  serverPort,
			NetworkCIDR: networkCIDR,
		}
		err := stun.RunServer(ctx, tun, cfg)
		if err != nil {
			panic(err)
		}
	} else {
		runClient(ctx, tun, serverIP, serverPort, clientPort, networkCIDR, dnsServer)
	}

	handleInterrupt(cancel)

	<-ctx.Done()

	log.Info("shutdown")

	if err := ctx.Err(); err != nil && err != context.Canceled {
		panic(err)
	}
}

func parseHostAndPort(peerEndpoint string) (string, int, error) {
	var ip string
	var port int
	var err error
	parts := strings.SplitN(peerEndpoint, ":", 2)
	ip = parts[0]
	if len(parts) > 1 {
		port, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", 0, err
		}
	}
	return ip, port, nil
}

func handleInterrupt(cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		for sig := range sigCh {
			if sig == os.Interrupt {
				cancel()
			}
		}
	}()
}

func runClient(ctx context.Context, tun stun.TunDevice, serverIP string, serverPort int, clientPort int, networkCIDR string, s string) {
	cfg := stun.ClientConfig{
		ServerPort:            serverPort,
		ServerInternetAddress: serverIP,
		ClientPort:            clientPort,
		NetworkCIDR:           networkCIDR,
	}
	err := stun.RunClient(ctx, tun, cfg)
	if err != nil {
		panic(err)
	}

	if len(forceRouteDomains) > 0 {
		f, err := os.Open(forceRouteDomains)
		if err != nil {
			panic(err)
		}

		if err := stun.KeepRoutesToDomains(ctx, tun, dnsServer, f); err != nil {
			f.Close()
			panic(err)
		}
		f.Close()
	}
}
