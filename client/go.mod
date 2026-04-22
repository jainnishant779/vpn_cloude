module quicktunnel/client

go 1.21

require (
	github.com/pion/stun v0.6.1
	github.com/rs/zerolog v1.33.0
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	golang.zx2c4.com/wireguard v0.0.0-20231211153847-12269c276173
	quicktunnel.local/pkg v0.0.0
)

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/pion/dtls/v2 v2.2.7 // indirect
	github.com/pion/logging v0.2.2 // indirect
	github.com/pion/transport/v2 v2.2.1 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	golang.org/x/crypto v0.25.0 // indirect
	golang.org/x/net v0.21.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
)

replace quicktunnel.local/pkg => ../pkg
