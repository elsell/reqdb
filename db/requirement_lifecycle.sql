ALTER TABLE requirement
ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active'
CHECK (lifecycle_state IN ('active', 'retired'));
