package workspace

import (
	"context"
	"strings"
)

type Client interface {
	Exists(ctx context.Context, name string) (bool, error)
	Create(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
}

type ContainerSpec struct {
	Name       string
	Image      string
	DataVolume string
	Labels     map[string]string
}

type Manager struct {
	Client      Client
	Image       string
	DataVolume  string
	ContainerID string
}

func (m Manager) Ensure(ctx context.Context, userID string) error {
	name := m.ContainerID
	if name == "" {
		name = "vibe-workspace-" + sanitize(userID)
	}
	exists, err := m.Client.Exists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		if err := m.Client.Create(ctx, ContainerSpec{
			Name:       name,
			Image:      m.Image,
			DataVolume: m.DataVolume,
			Labels: map[string]string{
				"vibe-terminal.user_id": userID,
			},
		}); err != nil {
			return err
		}
	}
	return m.Client.Start(ctx, name)
}

func sanitize(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, ".", "-")
	return value
}

type FakeClient struct {
	Existing map[string]bool
	Created  map[string]bool
	Started  map[string]bool
}

func NewFakeClient() *FakeClient {
	return &FakeClient{
		Existing: map[string]bool{},
		Created:  map[string]bool{},
		Started:  map[string]bool{},
	}
}

func (c *FakeClient) Exists(ctx context.Context, name string) (bool, error) {
	return c.Existing[name] || c.Created[name], nil
}

func (c *FakeClient) Create(ctx context.Context, spec ContainerSpec) error {
	c.Created[spec.Name] = true
	return nil
}

func (c *FakeClient) Start(ctx context.Context, name string) error {
	c.Started[name] = true
	return nil
}
