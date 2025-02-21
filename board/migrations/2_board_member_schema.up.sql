-- Board Members Table
CREATE TABLE board_members (
    board_id UUID REFERENCES boards(id) ON DELETE CASCADE,  -- Board ID
    user_id UUID NOT NULL,   -- User ID from User Service
    role VARCHAR(20) CHECK (role IN ('Admin', 'Member', 'Viewer')) NOT NULL,
    PRIMARY KEY (board_id, user_id)
);

CREATE UNIQUE INDEX unique_admin_per_board 
ON board_members (board_id) 
WHERE role = 'Admin';