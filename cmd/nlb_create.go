package cmd

import (
	"fmt"
	"strings"

	exov2 "github.com/exoscale/egoscale/v2"
	exoapi "github.com/exoscale/egoscale/v2/api"
	"github.com/spf13/cobra"
)

type nlbCreateCmd struct {
	_ bool `cli-cmd:"create"`

	Name string `cli-arg:"#" cli-usage:"NAME"`

	Description string            `cli-usage:"Network Load Balancer description"`
	Labels      map[string]string `cli-flag:"label" cli-usage:"Network Load Balancer label (format: key=value)"`
	Zone        string            `cli-short:"z" cli-usage:"Network Load Balancer zone"`
}

func (c *nlbCreateCmd) cmdAliases() []string { return gCreateAlias }

func (c *nlbCreateCmd) cmdShort() string { return "Create a Network Load Balancer" }

func (c *nlbCreateCmd) cmdLong() string {
	return fmt.Sprintf(`This command creates a Network Load Balancer.

Supported output template annotations: %s`,
		strings.Join(outputterTemplateAnnotations(&nlbShowOutput{}), ", "))
}

func (c *nlbCreateCmd) cmdPreRun(cmd *cobra.Command, args []string) error {
	cmdSetZoneFlagFromDefault(cmd)
	return cliCommandDefaultPreRun(c, cmd, args)
}

func (c *nlbCreateCmd) cmdRun(_ *cobra.Command, _ []string) error {
	nlb := &exov2.NetworkLoadBalancer{
		Description: c.Description,
		Labels:      c.Labels,
		Name:        c.Name,
	}

	ctx := exoapi.WithEndpoint(gContext, exoapi.NewReqEndpoint(gCurrentAccount.Environment, c.Zone))

	var err error
	decorateAsyncOperation(fmt.Sprintf("Creating Network Load Balancer %q...", nlb.Name), func() {
		nlb, err = cs.CreateNetworkLoadBalancer(ctx, c.Zone, nlb)
	})
	if err != nil {
		return err
	}

	if !gQuiet {
		return output(showNLB(c.Zone, nlb.ID))
	}

	return nil
}

func init() {
	cobra.CheckErr(registerCLICommand(nlbCmd, &nlbCreateCmd{}))
}
