-- The fixed "Text channels"/"Voice channels" groups become real,
-- editable categories. Spaces with uncategorised channels get a category
-- per kind (German names: existing installs are German; new spaces get
-- the creator's language from the client). They sort before all other
-- categories, as the fixed groups did.
WITH m AS MATERIALIZED (SELECT COALESCE(MAX(id), 0) AS mx FROM channels),
need AS (
  SELECT s.id AS space_id, k.kind, k.name, k.pos, s.created_at
  FROM spaces s
  JOIN (SELECT 'text' AS kind, 'Textkanäle' AS name, -2 AS pos
        UNION ALL SELECT 'voice', 'Sprachkanäle', -1) k
  WHERE EXISTS (SELECT 1 FROM channels c WHERE c.space_id = s.id AND c.kind = k.kind AND c.parent_id IS NULL)
)
INSERT INTO channels(id, space_id, kind, name, position, created_at)
SELECT m.mx + ROW_NUMBER() OVER (ORDER BY need.space_id, need.pos), need.space_id, 'category', need.name, need.pos, need.created_at
FROM need, m;

UPDATE channels SET parent_id = (
  SELECT c.id FROM channels c
  WHERE c.space_id = channels.space_id AND c.kind = 'category'
    AND c.position = CASE channels.kind WHEN 'text' THEN -2 ELSE -1 END
)
WHERE kind IN ('text', 'voice') AND parent_id IS NULL AND space_id IS NOT NULL;
