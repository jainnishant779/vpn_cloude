CREATE TABLE IF NOT EXISTS relay_servers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(50) NOT NULL,
    region       VARCHAR(30) NOT NULL,
    hostname     VARCHAR(255) NOT NULL,
    ip           INET NOT NULL,
    port         INT NOT NULL DEFAULT 3478,
    is_healthy   BOOLEAN NOT NULL DEFAULT true,
    current_load INT NOT NULL DEFAULT 0,
    max_load     INT NOT NULL DEFAULT 1000,
    latitude     DECIMAL(9,6),
    longitude    DECIMAL(9,6),
    created_at   TIMESTAMP NOT NULL DEFAULT NOW()
);
