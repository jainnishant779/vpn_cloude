module quicktunnel/client

go 1.21

require (
	github.com/pion/stun v0.6.1
	quicktunnel.local/pkg v0.0.0
	github.com/rs/zerolog v1.33.0
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	golang.zx2c4.com/wireguard v0.0.0-20231211153847-12269c276173
)

replace quicktunnel.local/pkg => ../pkg
