module quicktunnel/server

go 1.21

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jackc/pgx/v5 v5.5.5
	quicktunnel.local/pkg v0.0.0
	github.com/redis/go-redis/v9 v9.6.1
	github.com/rs/zerolog v1.33.0
	github.com/stretchr/testify v1.9.0
	golang.org/x/crypto v0.25.0
)

replace quicktunnel.local/pkg => ../pkg
