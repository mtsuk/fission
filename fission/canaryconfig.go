/*
Copyright 2016 The Fission Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fission/fission"
	"github.com/fission/fission/crd"
)

func canaryConfigCreate(c *cli.Context) error {
	client := getClient(c.GlobalString("server"))

	canaryConfigName := c.String("name")
	if len(canaryConfigName) == 0 {
		fatal("Need a name, use --name.")
	}

	// TODO : Allow namespace
	ns := "default"

	//m := &metav1.ObjectMeta{
	//	Name:      canaryConfigName,
	//	Namespace: ns,
	//}

	//canaryCfg, err := client.CanaryConfigGet(m)
	//if  canaryCfg != nil {
	//	checkErr(fmt.Errorf("duplicate canary config"), "Another canary config with the same name exists")
	//}

	trigger := c.String("trigger")
	funcN := c.String("funcN")
	funcNminus1 := c.String("funcN-1")
	incrementInterval:= c.String("increment-interval")
	incrementStep := c.Int("increment-step")
	failureThreshold := c.Int("failure-threshold")


	// TODO : Validation check for time parsing

	canaryCfg := &crd.CanaryConfig{
		Metadata: metav1.ObjectMeta{
			Name:      canaryConfigName,
			Namespace: ns,
		},
		Spec: fission.CanaryConfigSpec{
			Trigger: fission.TriggerReference {
				Name: trigger,
				Namespace: ns,
			},
			FunctionN: funcN,
			FunctionNminus1: funcNminus1,
			WeightIncrement: incrementStep,
			WeightIncrementDuration:  incrementInterval,
			FailureThreshold: failureThreshold,
			FailureType: fission.FailureTypeStatusCode,
		},
	}

	_, err := client.CanaryConfigCreate(canaryCfg)
	checkErr(err, "create canary config")

	fmt.Printf("canary config '%v' created\n", canaryConfigName)
	return err
}

func canaryConfigGet(c *cli.Context) error {
	client := getClient(c.GlobalString("server"))

	name := c.String("name")
	if len(name) == 0 {
		fatal("Need a name, use --name.")
	}
	ns := c.String("namespace")
	if ns == "" {
		ns = "default"
	}

	m := &metav1.ObjectMeta{
		Name:      name,
		Namespace: ns,
	}

	canaryCfg, err := client.CanaryConfigGet(m)
	checkErr(err, "get canary config")


	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\n", "NAME", "TRIGGER", "FUNCTION-N", "FUNCTION-N-1", "WEIGHT-INCREMENT", "INTERVAL", "FAILURE-THRESHOLD", "FAILURE-TYPE")
	fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\t%v\t%v\n",
		canaryCfg.Metadata.Name, canaryCfg.Spec.Trigger, canaryCfg.Spec.FunctionN, canaryCfg.Spec.FunctionNminus1, canaryCfg.Spec.WeightIncrement, canaryCfg.Spec.WeightIncrementDuration,
			canaryCfg.Spec.FailureThreshold, canaryCfg.Spec.FailureType)

	w.Flush()
	return nil
}