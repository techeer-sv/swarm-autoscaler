package docker

import (
	"github.com/moby/moby/client"
)

func New() (*client.Client, error) {
	return client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
}
