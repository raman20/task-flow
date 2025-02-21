-- Boards Table
CREATE TABLE boards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT,
    created_by UUID NOT NULL,  -- User ID from User Service
    created_at TIMESTAMP DEFAULT NOW()
);