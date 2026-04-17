-- 007_member_approval.sql
-- Adds ZeroTier-style member approval workflow.
--
-- Networks gain an access_control mode:
--   'approve' = new peers need admin approval (default, matches ZeroTier)
--   'auto'    = any peer with the network ID joins instantly
--
-- Peers gain a membership status and a member_token for device-level auth
-- so the CLI can poll for approval without needing the user's API key.

ALTER TABLE networks
    ADD COLUMN IF NOT EXISTS access_control VARCHAR(10) NOT NULL DEFAULT 'approve';

ALTER TABLE peers
    ADD COLUMN IF NOT EXISTS status VARCHAR(10) NOT NULL DEFAULT 'approved';

ALTER TABLE peers
    ADD COLUMN IF NOT EXISTS member_token VARCHAR(128) UNIQUE;

CREATE INDEX IF NOT EXISTS idx_peers_network_status ON peers(network_id, status);
