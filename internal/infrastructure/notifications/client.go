package notifications

import (
	"context"

	notificationsv1 "github.com/manovaspace/orbit-notifications/api/notifications/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
