# stun

Dumb vpn implementation on L3 level and udp as
client server transport protocol.

Server support only linux.
Client support mac os x and linux.

## Build
```bash
make build
```
## Run
Server
```bash
./stun -server -n=192.168.50.1/24
```

Client
```bash
sudo ./stun -p 100.100.100.100:1300  -n 192.168.50.5/24   -f domains.csv
```
