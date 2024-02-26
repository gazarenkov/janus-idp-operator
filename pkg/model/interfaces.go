//
// Copyright (c) 2023 Red Hat, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package model

import (
	bsv1alpha1 "janus-idp.io/backstage-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Registered Object configuring Backstage deployment
type ObjectConfig struct {
	// Factory to create the object
	ObjectFactory ObjectFactory
	// Unique key identifying the "kind" of Object which also is the name of config file.
	// For example: "deployment.yaml" containing configuration of Backstage Deployment
	Key string
}

type ObjectFactory interface {
	newBackstageObject() RuntimeObject
}

// Abstraction for the model Backstage object taking part in deployment
type RuntimeObject interface {
	// Object underlying Kubernetes object
	Object() client.Object
	// setObject sets object
	setObject(obj client.Object, backstageName string)
	// EmptyObject an empty object the same kind as Object
	EmptyObject() client.Object
	// adds runtime object to the model and generates default metadata for future applying
	addToModel(model *BackstageModel, backstageMeta bsv1alpha1.Backstage, ownsRuntime bool) (bool, error)
	// at this stage all the information is updated
	// set the final references validates the object at the end of initialization
	validate(model *BackstageModel, backstage bsv1alpha1.Backstage) error
	// sets object name, labels and other necessary meta information
	setMetaInfo(backstageName string)
}

// BackstagePodContributor contributing to the pod as an Environment variables or mounting file/directory.
// Usually app-config related
type BackstagePodContributor interface {
	RuntimeObject
	updatePod(deployment *appsv1.Deployment)
}
