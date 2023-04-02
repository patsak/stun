# stun

Basic vpn implementation with tun devices and udp for 
client server transport protocol.

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
sudo ./stun -p 206.81.27.172:1300  -n 192.168.50.5/24   -f domains.csv
```
