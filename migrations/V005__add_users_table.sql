-- Users table for authentication
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL;

-- Insert a default admin user (password: admin123)
-- Password hash is bcrypt hash of "admin123" with cost 10
-- You should change this password after first login
INSERT INTO users (id, username, email, password_hash) 
VALUES (
    'admin',
    'admin',
    'admin@example.com',
    '$2a$10$PjMH8x54x3Ersl57dyiAQ.2n6KpsXtv6sp3GR6U73C2d/.5xwoytu'
) ON CONFLICT (username) DO NOTHING;

