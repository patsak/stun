FROM alpine:3.14

WORKDIR /app

COPY stun-linux /app/stun

RUN apk add iptables tcpdump

ENTRYPOINT [ "/app/stun" ]
