package docker

import (
	"context"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

func ListServices(ctx context.Context, cli *client.Client) ([]swarm.Service, error) {
	result, err := cli.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}
