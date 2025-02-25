// user service manages user authentication and basic user operations such as signup,
// login, and retrieving user details. It uses a separate database for user data and
// integrates with JWT-based authentication.
package user

import (
	"context"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// userDB is the database instance for the user service, managing the users table.
var userDB = sqldb.NewDatabase("users", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// SignupParams defines the input parameters for user signup.
type SignupParams struct {
	Email    string `json:"email"`    // user email
	Password string `json:"password"` // user password
}

// SignupResponse represents the response returned when a user signs up successfully.
type SignupResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Signup registers a new user with an email and password, storing a hashed password.
//
//encore:api public method=POST path=/signup
func Signup(ctx context.Context, p *SignupParams) (*SignupResponse, error) {
	if p.Email == "" || p.Password == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("email and password are required").Err()
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to hash password").Cause(err).Err()
	}

	var id string
	err = userDB.QueryRow(ctx, `
        INSERT INTO users (email, password_hash)
        VALUES ($1, $2)
        RETURNING id
    `, p.Email, string(hash)).Scan(&id)
	if err != nil {
		if sqldb.ErrCode(err) == "23505" { // PostgreSQL unique violation
			return nil, errs.B().Code(errs.AlreadyExists).Msg("user already exists").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to create user").Cause(err).Err()
	}

	return &SignupResponse{ID: id, Email: p.Email}, nil
}

// LoginParams defines the input parameters for user login.
type LoginParams struct {
	Email    string `json:"email"`    // user email
	Password string `json:"password"` // user password
}

// LoginResponse represents the response returned when a user logs in, containing a JWT token.
type LoginResponse struct {
	Token string `json:"token"`
}

// Login authenticates a user and returns a JWT token valid for 24 hours.
//
//encore:api public method=POST path=/login
func Login(ctx context.Context, p *LoginParams) (*LoginResponse, error) {
	if p.Email == "" || p.Password == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("email and password are required").Err()
	}

	var id, passwordHash string
	err := userDB.QueryRow(ctx, `
        SELECT id, password_hash
        FROM users
        WHERE email = $1
    `, p.Email).Scan(&id, &passwordHash)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return nil, errs.B().Code(errs.Unauthenticated).Msg("invalid email or password").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch user").Cause(err).Err()
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(p.Password)); err != nil {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("invalid email or password").Err()
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   id,
		"email": p.Email,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	})
	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to generate token").Cause(err).Err()
	}

	return &LoginResponse{Token: tokenString}, nil
}

var jwtSecret = []byte("JWT_SECRET_KEY")

// AuthHandler validates a JWT token from incoming requests and returns the authenticated
// user's UID. It is invoked automatically by Encore for APIs marked with `auth`.
//
//encore:authhandler
func AuthHandler(ctx context.Context, token string) (auth.UID, error) {
	if token == "" {
		return "", errs.B().Code(errs.Unauthenticated).Msg("token is required").Err()
	}

	claims := &jwt.MapClaims{}
	tkn, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errs.B().Code(errs.Unauthenticated).Msg("unexpected signing method").Err()
		}
		return jwtSecret, nil
	})

	if err != nil || !tkn.Valid {
		return "", errs.B().Code(errs.Unauthenticated).Msg("invalid or expired token").Cause(err).Err()
	}

	sub, ok := (*claims)["sub"].(string)
	if !ok || sub == "" {
		return "", errs.B().Code(errs.Unauthenticated).Msg("invalid token: missing or invalid user ID").Err()
	}

	return auth.UID(sub), nil
}
