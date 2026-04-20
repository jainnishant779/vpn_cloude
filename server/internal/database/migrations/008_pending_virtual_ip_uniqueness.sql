-- Allow multiple pending peers to coexist before IP allocation.
-- Pending peers use 0.0.0.0 as a placeholder virtual_ip, so a strict
-- unique(network_id, virtual_ip) constraint blocks all but one pending join.

-- Drop prior strict uniqueness (constraint/index from older migrations/hotfixes).
ALTER TABLE peers
    DROP CONSTRAINT IF EXISTS peers_network_virtual_ip_unique;

DROP INDEX IF EXISTS peers_network_virtual_ip_unique;

-- Enforce uniqueness only for real assigned IPs.
CREATE UNIQUE INDEX IF NOT EXISTS peers_network_virtual_ip_unique
    ON peers (network_id, virtual_ip)
    WHERE virtual_ip IS NOT NULL
      AND virtual_ip <> '0.0.0.0'::inet;
