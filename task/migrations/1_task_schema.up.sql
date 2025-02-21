CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,  -- Reference to Board (from Board Service)
    title TEXT NOT NULL,
    description TEXT,
    created_by UUID NOT NULL,  -- User ID (from User Service)
    assignee_id UUID,  -- User ID (from User Service) (nullable)
    stage VARCHAR(20) CHECK (stage IN ('To Do', 'In Progress', 'Done')) DEFAULT 'To Do',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
