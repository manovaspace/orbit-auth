package notifications

import (
	"context"
	"strconv"
	"time"

	notificationsv1 "github.com/manovaspace/orbit-notifications/api/notifications/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// Client sends OTP via orbit-notifications gRPC.
type Client struct {
	api notificationsv1.NotificationsServiceClient
}

func NewClient(addr string, opts ...grpc.DialOption) (*Client, error) {
	base := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	opts = append(base, opts...)
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{api: notificationsv1.NewNotificationsServiceClient(conn)}, nil
}

func (c *Client) SendOTP(ctx context.Context, channel, recipient, code, correlationID string) error {
	template := "otp_login"
	if channel == "sms" {
		template = "otp_login_sms"
	}
	_, err := c.api.Send(ctx, &notificationsv1.SendRequest{
		Template:      template,
		Channel:       channel,
		Recipient:     recipient,
		CorrelationId: correlationID,
		Vars:          map[string]string{"code": code},
	})
	return err
}

// SendSignupVerification asks orbit-notifications to render and deliver the
// signup_verify email template. The template itself lives in
// orbit-notifications; this adapter only supplies its documented variables.
func (c *Client) SendSignupVerification(ctx context.Context, recipient, code string, expiresInMinutes int, locale, correlationID string) error {
	if locale != "fa" && locale != "en" {
		locale = "en"
	}
	_, err := c.api.Send(ctx, &notificationsv1.SendRequest{
		Template:      "signup_verify",
		Channel:       "email",
		Recipient:     recipient,
		CorrelationId: correlationID,
		Vars: map[string]string{
			"code":               code,
			"expires_in_minutes": strconv.Itoa(expiresInMinutes),
			"locale":             locale,
		},
	})
	return err
}

// SendOrgInvite asks orbit-notifications to render and deliver the org_invite
// email template. The template itself lives in orbit-notifications; this
// adapter only supplies its documented variables.
func (c *Client) SendOrgInvite(ctx context.Context, invite domain.InviteNotification) error {
	locale := invite.Locale
	if locale != "fa" && locale != "en" {
		locale = "fa"
	}
	_, err := c.api.Send(ctx, &notificationsv1.SendRequest{
		Template:      "org_invite",
		Channel:       "email",
		Recipient:     invite.Recipient,
		CorrelationId: invite.CorrelationID,
		Vars: map[string]string{
			"org_name":     invite.OrgName,
			"inviter_name": invite.InviterName,
			"accept_token": invite.AcceptToken,
			"expires_at":   invite.ExpiresAt.UTC().Format(time.RFC3339),
			"locale":       locale,
		},
	})
	return err
}
