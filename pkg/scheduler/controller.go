// Copyright 2017 Mirantis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package scheduler

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/Mirantis/k8s-AppController/pkg/client"

	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/fields"
	"k8s.io/client-go/pkg/labels"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

const (
	controlName    = "AppcontrollerDeployment"
	concurrencyKey = "concurrency"
	selector       = "selector"
)

func singleObjectOpts() v1.ListOptions {
	return v1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", controlName).String(),
	}
}

func NewDeploymentController(appc client.Interface) cache.ControllerInterface {
	opts := singleObjectOpts()
	var stopChan chan struct{}
	_, cfgController := cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return appc.ConfigMaps().List(opts)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return appc.ConfigMaps().Watch(opts)
			},
		},
		&api.ConfigMap{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				cfg := obj.(*api.ConfigMap)
				stopChan = make(chan struct{})
				var concurrency int
				concurrencyVal, ok := cfg.Data[concurrencyKey]
				if !ok {
					concurrency = 1
				} else {
					var err error
					concurrency, err = strconv.Atoi(concurrencyVal)
					if err != nil {
						fmt.Println(err)
					}
					return
				}
				labelSelector, ok := cfg.Data[selector]
				if !ok {
					labelSelector = ""
				}
				depGraph := initializeDependencyGraph(appc, labelSelector)
				fmt.Println("Running deployment with labels ", labelSelector)
				go Create(depGraph, concurrency, stopChan)
			},
			DeleteFunc: func(_ interface{}) {
				close(stopChan)
			},
		},
	)
	return cfgController
}

func initializeDependencyGraph(c client.Interface, labelSelector string) DependencyGraph {
	sel, err := labels.Parse(labelSelector)
	if err != nil {
		log.Println(err)
	}
	log.Println("Using label selector:", labelSelector)

	depGraph, err := BuildDependencyGraph(c, sel)
	if err != nil {
		log.Println(err)
	}

	log.Println("Checking for circular dependencies.")
	cycles := DetectCycles(depGraph)
	if len(cycles) > 0 {
		message := "Cycles detected, terminating:\n"
		for _, cycle := range cycles {
			keys := make([]string, 0, len(cycle))
			for _, vertex := range cycle {
				keys = append(keys, vertex.Key())
			}
			message = fmt.Sprintf("%sCycle: %s\n", message, strings.Join(keys, ", "))
		}

		log.Println(message)
	} else {
		log.Println("No cycles detected.")
	}
	return depGraph
}
