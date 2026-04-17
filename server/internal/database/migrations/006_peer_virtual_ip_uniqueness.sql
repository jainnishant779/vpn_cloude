-- Remove duplicate virtual IP assignments inside the same network.
-- Keep the most recently active row and delete older conflicting rows.
WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY network_id, virtual_ip
            ORDER BY
                is_online DESC,
                COALESCE(last_seen, created_at) DESC,
                created_at DESC,
                id DESC
        ) AS rn
    FROM peers
)
DELETE FROM peers p
USING ranked r
WHERE p.id = r.id
  AND r.rn > 1;

-- Enforce per-network virtual IP uniqueness for future registrations.
ALTER TABLE peers
ADD CONSTRAINT peers_network_virtual_ip_unique UNIQUE (network_id, virtual_ip);
