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

package canaryconfigmgr

import (
	"log"

	k8sCache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/fission/fission/crd"
	"time"
	"k8s.io/apimachinery/pkg/fields"
	"context"
)

type canaryConfigMgr struct {
	fissionClient     *crd.FissionClient
	kubeClient        *kubernetes.Clientset
	canaryConfigStore         k8sCache.Store
	canaryConfigController    k8sCache.Controller
	promClient *PrometheusApiClient
	crdClient         *rest.RESTClient
	canaryCfgCancelFuncMap *canaryConfigCancelFuncMap
}

func MakeCanaryConfigMgr(fissionClient *crd.FissionClient, kubeClient *kubernetes.Clientset, crdClient *rest.RESTClient) (*canaryConfigMgr) {
	configMgr := &canaryConfigMgr{
		fissionClient: fissionClient,
		kubeClient: kubeClient,
		crdClient: crdClient,
		// TODO : Remove this hard code after testing and have a check for prometheus service being up
		promClient: MakePrometheusClient("http://smelly-wildebeest-prometheus-server"),
	}

	store, controller := configMgr.initCanaryConfigController()
	configMgr.canaryConfigStore = store
	configMgr.canaryConfigController = controller

	//log.Printf("Invoking promClient.GetFunctionFailurePercentage")
	//configMgr.promClient.GetFunctionFailurePercentage("hello-nodejs", "default", 25 * time.Minute)
	//log.Printf("Finished invoking promClient.GetFunctionFailurePercentage")

	return configMgr
}


func(canaryCfgMgr *canaryConfigMgr) initCanaryConfigController() (k8sCache.Store, k8sCache.Controller) {
	resyncPeriod := 30 * time.Second
	listWatch := k8sCache.NewListWatchFromClient(canaryCfgMgr.crdClient, "canaryconfigs", metav1.NamespaceAll, fields.Everything())
	store, controller := k8sCache.NewInformer(listWatch, &crd.CanaryConfig{}, resyncPeriod,
		k8sCache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				canaryConfig := obj.(*crd.CanaryConfig)
				go canaryCfgMgr.addCanaryConfig(canaryConfig)
			},
			DeleteFunc: func(obj interface{}) {
				canaryConfig := obj.(*crd.CanaryConfig)
				go canaryCfgMgr.deleteCanaryConfig(canaryConfig)
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldConfig := oldObj.(*crd.CanaryConfig)
				newConfig := newObj.(*crd.CanaryConfig)
				if oldConfig.Metadata.ResourceVersion != newConfig.Metadata.ResourceVersion {
					go canaryCfgMgr.updateCanaryConfig(oldConfig, newConfig)
				}
				go canaryCfgMgr.reSyncCanaryConfigs()

			},
		})
	return store, controller
}

func(canaryCfgMgr *canaryConfigMgr) addCanaryConfig(canaryConfig *crd.CanaryConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	canaryCfgMgr.canaryCfgCancelFuncMap.assign(&canaryConfig.Metadata, &cancel)
	canaryCfgMgr.processCanaryConfig(&ctx, canaryConfig)
}

func(canaryCfgMgr *canaryConfigMgr) processCanaryConfig(ctx *context.Context, canaryConfig *crd.CanaryConfig) {
	ticker := time.NewTicker(canaryConfig.Spec.WeightIncrementDuration)
	quit := make(chan struct{})

	for {
		select {
		case <- (*ctx).Done():
			// this case when someone deleted their canary config in the middle of it being processed
			ticker.Stop()
			return

		case <- ticker.C:
			// every weightIncrementDuration, check if failureThreshold has reached.
			// if yes, rollback.
			// else, increment the weight of funcN and decrement funcN-1 by `weightIncrement`
			canaryCfgMgr.IncrementWeightOrRollback(canaryConfig, quit)

		case <- quit:
			// we're done processing this canary config either because the new function receives 100% of the traffic
			// or we rolled back to send all 100% traffic to old function
			ticker.Stop()
			return
		}
	}
}

func(canaryCfgMgr *canaryConfigMgr) IncrementWeightOrRollback(canaryConfig *crd.CanaryConfig, quit chan struct{}) {
	// get the http trigger object associated with this canary config
	triggerObj, err := canaryCfgMgr.getHttpTriggerObject(canaryConfig.Spec.Trigger.Name, canaryConfig.Spec.Trigger.Namespace)
	if err != nil {
		// just silently ignore. wait for next window to increment weight
		log.Printf("Error fetching http trigger object, err : %v", err)
		return
	}

	failurePercent, err := canaryCfgMgr.promClient.GetFunctionFailurePercentage(triggerObj.Spec.RelativeURL, triggerObj.Spec.Method,
		canaryConfig.Spec.FunctionN, canaryConfig.Metadata.Namespace, canaryConfig.Spec.WeightIncrementDuration)
	if err != nil {
		// just silently ignore. wait for next window to increment weight
		log.Printf("Error calculating failure percentage, err : %v", err)
		return
	}

	if failurePercent == -1 {
		// this means there were no requests triggered to this url during this window. return here and check back
		// during next iteration
		log.Printf("Total requests received for url : %v is 0", triggerObj.Spec.RelativeURL)
		return
	}

	// TODO : The right thing to do here is not pass the trigger object. because we might run into `StatusConflict` issue
	// change it to do a get and then update inside rollback
	if int(failurePercent) >= canaryConfig.Spec.FailureThreshold {
		canaryCfgMgr.rollback(canaryConfig, triggerObj)
		close(quit)
		return
	}

	doneProcessingCanaryConfig, err := canaryCfgMgr.incrementWeights(canaryConfig, triggerObj)
	if err != nil {
		// just log the error and hope that next iteration will succeed
		log.Printf("Error incrementing weights for triggerObj : %v, err : %v", triggerObj.Metadata.Name, err)
		return
	}
	if doneProcessingCanaryConfig {
		log.Printf("We're done processing canary config : %s. The new function is receiving all the traffic", canaryConfig.Metadata.Name)
		close(quit)
		return
	}

}

func(canaryCfgMgr *canaryConfigMgr) getHttpTriggerObject(triggerName, triggerNamespace string) (*crd.HTTPTrigger, error) {
	// TODO : Add retries
	return canaryCfgMgr.fissionClient.HTTPTriggers(triggerNamespace).Get(triggerName)
}

func(canaryCfgMgr *canaryConfigMgr) rollback(canaryConfig *crd.CanaryConfig, trigger *crd.HTTPTrigger) error {
	// TODO : Add retries
	functionWeights := trigger.Spec.FunctionReference.FunctionWeights
	functionWeights[canaryConfig.Spec.FunctionN] = 0
	functionWeights[canaryConfig.Spec.FunctionNminus1] = 100

	trigger.Spec.FunctionReference.FunctionWeights = functionWeights

	_, err := canaryCfgMgr.fissionClient.HTTPTriggers(trigger.Metadata.Namespace).Update(trigger)
	if err != nil {
		log.Printf("Error updating http trigger object, err : %v", err)
		return err
	}

	return nil
}

func(canaryCfgMgr *canaryConfigMgr) incrementWeights(canaryConfig *crd.CanaryConfig, trigger *crd.HTTPTrigger) (bool, error) {
	// TODO : Add retries
	doneProcessingCanaryConfig := false

	functionWeights := trigger.Spec.FunctionReference.FunctionWeights
	if functionWeights[canaryConfig.Spec.FunctionN] + canaryConfig.Spec.WeightIncrement > 100 {
		doneProcessingCanaryConfig = true
		functionWeights[canaryConfig.Spec.FunctionN] = 100
		functionWeights[canaryConfig.Spec.FunctionNminus1] = 0
	} else {
		functionWeights[canaryConfig.Spec.FunctionN] += canaryConfig.Spec.WeightIncrement
		if functionWeights[canaryConfig.Spec.FunctionNminus1] - canaryConfig.Spec.WeightIncrement < 0 {
			functionWeights[canaryConfig.Spec.FunctionNminus1] = 0
		} else {
			functionWeights[canaryConfig.Spec.FunctionNminus1] -= canaryConfig.Spec.WeightIncrement
		}
	}

	trigger.Spec.FunctionReference.FunctionWeights = functionWeights

	_, err := canaryCfgMgr.fissionClient.HTTPTriggers(trigger.Metadata.Namespace).Update(trigger)
	if err != nil {
		log.Printf("Error updating http trigger object, err : %v", err)
		return doneProcessingCanaryConfig, err
	}

	return doneProcessingCanaryConfig, nil
}

func(canaryCfgMgr *canaryConfigMgr) reSyncCanaryConfigs() {
	for _, canaryConfig := range canaryCfgMgr.canaryConfigStore.List() {
		cancelFunc, err := canaryCfgMgr.canaryCfgCancelFuncMap.lookup(&canaryConfig.(*crd.CanaryConfig).Metadata)
		if err != nil || cancelFunc == nil {
			// new canaryConfig detected, add it to our cache and start processing it
			go canaryCfgMgr.addCanaryConfig(canaryConfig.(*crd.CanaryConfig))
		}
	}
}

func(canaryCfgMgr *canaryConfigMgr) deleteCanaryConfig(canaryConfig *crd.CanaryConfig) {
	cancelFunc, err := canaryCfgMgr.canaryCfgCancelFuncMap.lookup(&canaryConfig.Metadata)
	if err != nil {
		log.Printf("Something's wrong, lookup of canaryConfig failed, err : %v", err)
		return
	}
	// when this is called, the ctx.Done returns inside processCanaryConfig function and processing gets stopped
	(*cancelFunc)()
}


func(canaryCfgMgr *canaryConfigMgr) updateCanaryConfig(oldCanaryConfig *crd.CanaryConfig, newCanaryConfig *crd.CanaryConfig) {
	err := canaryCfgMgr.canaryCfgCancelFuncMap.remove(&oldCanaryConfig.Metadata)
	if err != nil {
		log.Printf("Something's wrong, error removing canary config: %s from map, err : %v", oldCanaryConfig.Metadata.Name, err)
		return
	}
	canaryCfgMgr.addCanaryConfig(newCanaryConfig)
}
