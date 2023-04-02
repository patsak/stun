package stun

type ClientConfig struct {
	NetworkCIDR           string
	ClientPort            int
	ServerInternetAddress string
	ServerPort            int
}

type ServerConfig struct {
	ServerPort  int
	NetworkCIDR string
}
