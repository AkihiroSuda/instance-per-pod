package main

import (
	"errors"
	"net/http"

	"github.com/urfave/cli"
	"k8s.io/klog"

	"github.com/AkihiroSuda/instance-per-pod/pkg/mutator"
	"github.com/AkihiroSuda/instance-per-pod/pkg/webhook"
)

var webhookCommand = cli.Command{
	Name:      "webhook",
	Usage:     "start admission webhook daemon",
	ArgsUsage: "[flags]",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "addr",
			Usage: "address",
			Value: ":443",
		},
		cli.StringFlag{
			Name:  "tlscert",
			Usage: "tls cert file",
		},
		cli.StringFlag{
			Name:  "tlskey",
			Usage: "tls key file",
		},
		cli.StringFlag{
			Name:  "node-label",
			Usage: "The node label used for the IPP autoscaling node pool. The label value should be \"true\".",
			Value: "ipp",
		},
		cli.StringFlag{
			Name:  "node-taint",
			Usage: "The node taint used for the IPP autoscaling node pool. The taint value should be \"true\".",
			Value: "ipp",
		},
		cli.StringFlag{
			Name:  "pod-label",
			Usage: "The pod label. The label value can be an arbitrary string.",
			Value: "ipp-class",
		},
	},
	Action: webhookAction,
}

func webhookAction(clicontext *cli.Context) error {
	addr := clicontext.String("addr")
	tlsCert := clicontext.String("tlscert")
	tlsKey := clicontext.String("tlskey")

	if tlsCert == "" {
		return errors.New("tlscert needs to be specified")
	}
	if tlsKey == "" {
		return errors.New("tlskey needs to be specified")
	}
	mutator := &mutator.BasicMutator{
		NodeLabel: clicontext.String("node-label"),
		NodeTaint: clicontext.String("node-taint"),
		PodLabel:  clicontext.String("pod-label"),
	}
	handlerFunc := webhook.HandlerFunc(mutator)
	const serverPath = "/admission"
	klog.Infof("webhook starting on %q (%q)", serverPath, addr)
	http.HandleFunc(serverPath, handlerFunc)
	return http.ListenAndServeTLS(addr, tlsCert, tlsKey, nil)
}
