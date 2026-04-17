CREATE TABLE IF NOT EXISTS peers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id      UUID NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    name            VARCHAR(100),
    machine_id      VARCHAR(64) UNIQUE NOT NULL,
    public_key      VARCHAR(44) NOT NULL,
    virtual_ip      INET NOT NULL,
    public_endpoint VARCHAR(50),
    local_endpoints TEXT[] NOT NULL DEFAULT '{}',
    os              VARCHAR(20),
    version         VARCHAR(20),
    is_online       BOOLEAN NOT NULL DEFAULT false,
    last_seen       TIMESTAMP,
    last_handshake  TIMESTAMP,
    rx_bytes        BIGINT NOT NULL DEFAULT 0,
    tx_bytes        BIGINT NOT NULL DEFAULT 0,
    vnc_port        INT NOT NULL DEFAULT 5900,
    vnc_available   BOOLEAN NOT NULL DEFAULT false,
    relay_id        VARCHAR(50),
    created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_peers_network ON peers(network_id);
CREATE INDEX IF NOT EXISTS idx_peers_online ON peers(is_online);
