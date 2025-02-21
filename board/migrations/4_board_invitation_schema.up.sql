-- Invitations Table
CREATE TABLE invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID REFERENCES boards(id) ON DELETE CASCADE,  -- Board ID
    inviter_id UUID NOT NULL,  -- Inviter (User ID from User Service)
    invitee_id UUID NOT NULL,  -- Invitee (User ID from User Service)
    role VARCHAR(20) CHECK (role IN ('Member', 'Viewer')) NOT NULL DEFAULT 'Viewer',
    status VARCHAR(10) CHECK (status IN ('Pending', 'Accepted', 'Rejected')) DEFAULT 'Pending',
    created_at TIMESTAMP DEFAULT NOW()
);
