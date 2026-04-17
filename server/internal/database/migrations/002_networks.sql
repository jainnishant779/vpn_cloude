CREATE TABLE IF NOT EXISTS networks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          VARCHAR(100) NOT NULL,
    network_id    VARCHAR(16) UNIQUE NOT NULL,
    cidr          VARCHAR(18) NOT NULL DEFAULT '10.7.0.0/16',
    description   TEXT,
    max_peers     INT NOT NULL DEFAULT 25,
    is_active     BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMP NOT NULL DEFAULT NOW()
);
