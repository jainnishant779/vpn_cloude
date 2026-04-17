CREATE TABLE IF NOT EXISTS connection_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    peer_a_id   UUID REFERENCES peers(id),
    peer_b_id   UUID REFERENCES peers(id),
    conn_type   VARCHAR(10) NOT NULL,
    relay_id    UUID REFERENCES relay_servers(id),
    latency_ms  INT,
    bandwidth   INT,
    started_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMP
);
