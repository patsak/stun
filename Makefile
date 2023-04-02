.PHONY: build build-linux docker-build docker-run

APP=stun

build:
	-rm $(APP)
	go build -o $(APP) cmd/main.go

build-linux:
	GOARCH=amd64 GOOS=linux go build -o $(APP)-linux cmd/main.go

docker-build: build-linux
	GOARCH=amd64 GOOS=linux go build -o $(APP)-linux cmd/main.go
	docker build --tag=stun .

docker-run:
	-docker network rm stun
	-docker network create stun  --subnet=172.22.0.1/16
	-docker kill stun-server
	docker run --name=stun-server \
 		--rm -d \
 		--network=stun \
 		--ip=172.22.0.5 \
 		-p 1300:1300/udp \
 		--device=/dev/net/tun \
 		--cap-add=NET_ADMIN \
 		 stun -server -tunN=5 -network-cidr=192.168.50.1/24 -verbose

	docker exec stun-server /bin/sh -c "\
		iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE"


	-docker kill stun-client
	docker run --name=stun-client \
 		--network=stun --rm -d \
 		--device=/dev/net/tun \
 		--cap-add=NET_ADMIN \
 		stun -tunN=5 --server-inet-address=172.22.0.5 --network-cidr=192.168.50.5/24 -verbose
	docker exec stun-client /bin/sh -c "\
		ip route del default ;\
		ip route add default dev tun5"

docker-ping:
	docker exec stun-client ping google.com