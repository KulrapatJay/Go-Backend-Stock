package http

import (
	"time"

	"go-backend-stock/internal/core/domain"
)

// PortfolioRequest is the body of POST and PUT /api/v1/portfolios. A missing description means "".
type PortfolioRequest struct {
	Name        string  `json:"name" example:"Long term" minLength:"1" maxLength:"100" validate:"required"`
	Description *string `json:"description" example:"Buy and hold, reviewed once a year." maxLength:"1000"`
}

// PortfolioResponse is one portfolio. Version is the number to send back in If-Match.
type PortfolioResponse struct {
	ID          string    `json:"id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d" validate:"required"`
	Name        string    `json:"name" example:"Long term" validate:"required"`
	Description string    `json:"description" example:"Buy and hold, reviewed once a year." validate:"required"`
	Currency    string    `json:"currency" enums:"USD" example:"USD" validate:"required"`
	Version     int32     `json:"version" example:"1" validate:"required"`
	CreatedAt   time.Time `json:"created_at" format:"date-time" example:"2026-10-29T09:30:00Z" validate:"required"`
	UpdatedAt   time.Time `json:"updated_at" format:"date-time" example:"2026-10-29T09:30:00Z" validate:"required"`
}

// PortfolioPageResponse is one page of portfolios, newest first. NextCursor is null on the last page.
type PortfolioPageResponse struct {
	Data       []PortfolioResponse `json:"data" validate:"required"`
	NextCursor *string             `json:"next_cursor" extensions:"x-nullable" example:"eyJrIjoicG9ydGZvbGlvcyJ9" validate:"required"`
}

func newPortfolioResponse(p domain.Portfolio) PortfolioResponse {
	return PortfolioResponse{
		ID: p.ID.String(), Name: p.Name.String(), Description: p.Description.String(), Currency: string(p.Currency),
		Version: p.Version.Int32(), CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
}

func newPortfolioPageResponse(page domain.PortfolioPage) PortfolioPageResponse {
	out := PortfolioPageResponse{Data: make([]PortfolioResponse, 0, len(page.Items))}
	for _, p := range page.Items {
		out.Data = append(out.Data, newPortfolioResponse(p))
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out
}
