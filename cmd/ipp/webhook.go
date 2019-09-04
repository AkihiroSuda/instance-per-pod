package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/golang/glog"
	"github.com/urfave/cli"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/AkihiroSuda/instance-per-pod/pkg/drivers"
	"github.com/AkihiroSuda/instance-per-pod/pkg/drivers/gke"
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
		// GKE driver
		cli.StringFlag{
			Name:  "gke-parent",
			Usage: "gke parent, specified in the format 'projects/*/locations/*/clusters/*'",
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
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	ctx := context.TODO()
	d, err := createDriver(ctx, clicontext, clientset)
	if err != nil {
		return err
	}
	go func() {
		derr := d.Run(ctx)
		if derr != nil {
			panic(derr)
		}
		glog.Info("driver routine stopped")
	}()
	mutator := &mutator.BasicMutator{Driver: d}
	handlerFunc := webhook.HandlerFunc(mutator)
	const serverPath = "/admission"
	glog.Infof("webhook starting on %q (%q)", serverPath, addr)
	http.HandleFunc(serverPath, handlerFunc)
	return http.ListenAndServeTLS(addr, tlsCert, tlsKey, nil)
}

func createDriver(ctx context.Context, clicontext *cli.Context, clientset *kubernetes.Clientset) (drivers.Driver, error) {
	gkeParent := clicontext.String("gke-parent")
	if gkeParent == "" {
		return nil, errors.New("gke-parent needs to be specified")
	}
	return gke.New(ctx, clientset, gkeParent)
}
