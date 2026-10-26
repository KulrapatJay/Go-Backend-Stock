package http

import (
	"time"

	"go-backend-stock/internal/core/domain"
)

// CSRFResponse is returned by GET /api/v1/auth/csrf.
type CSRFResponse struct {
	CSRFToken string    `json:"csrf_token" example:"AQAAAABnHk3QxJ9b3m0z0bH2l9V1cW5qQk1xU2ZtV0RkY0p0dVhQb0xMZ3hQYnpqUkw"`
	ExpiresAt time.Time `json:"expires_at" example:"2026-10-24T10:00:00Z"`
}

// otpAcceptedMessage is the same for every outcome, so it reveals nothing about the account.
const otpAcceptedMessage = "If this address can receive email, a verification code has been sent. It expires in 10 minutes."

// RegisterRequest is the body of POST /api/v1/auth/register.
type RegisterRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"Zq9!kVx1"`
}

// OTPAcceptedResponse answers register and "send the code again" with the same shape every time.
type OTPAcceptedResponse struct {
	Message     string `json:"message" example:"If this address can receive email, a verification code has been sent. It expires in 10 minutes."`
	ChallengeID string `json:"challenge_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	ExpiresIn   int    `json:"expires_in" example:"600"`
}

func newOTPAccepted(r domain.OTPReceipt) OTPAcceptedResponse {
	return OTPAcceptedResponse{Message: otpAcceptedMessage, ChallengeID: r.ChallengeID.String(), ExpiresIn: int(r.ExpiresIn / time.Second)}
}

// EmailRequest is the body of POST /api/v1/auth/email-verification/request.
type EmailRequest struct {
	Email string `json:"email" example:"user@example.com"`
}

// ConfirmEmailRequest is the body of POST /api/v1/auth/email-verification/confirm.
type ConfirmEmailRequest struct {
	ChallengeID string `json:"challenge_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	OTP         string `json:"otp" example:"042731"`
}

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"Zq9!kVx1"`
}

// UserResponse is an account as the API shows it. It never carries the password hash.
type UserResponse struct {
	ID            string    `json:"id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	Email         string    `json:"email" example:"user@example.com"`
	EmailVerified bool      `json:"email_verified" example:"true"`
	CreatedAt     time.Time `json:"created_at" example:"2026-10-24T09:30:00Z"`
}

func newUserResponse(u domain.User) UserResponse {
	return UserResponse{ID: u.ID.String(), Email: u.Email.String(), EmailVerified: u.EmailVerifiedAt != nil, CreatedAt: u.CreatedAt.UTC()}
}

// SessionResponse answers a successful login. The session itself travels only in the cookie.
type SessionResponse struct {
	User      UserResponse `json:"user"`
	ExpiresAt time.Time    `json:"expires_at" example:"2026-10-31T09:30:00Z"`
}
