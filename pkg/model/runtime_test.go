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
	"context"
	"fmt"
	"testing"

	"k8s.io/utils/pointer"

	"janus-idp.io/backstage-operator/api/v1alpha1"
	bsv1alpha1 "janus-idp.io/backstage-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

//const backstageContainerName = "backstage-backend"

// NOTE: to make it work locally env var LOCALBIN should point to the directory where default-config folder located
func TestInitDefaultDeploy(t *testing.T) {

	bs := v1alpha1.Backstage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bs",
			Namespace: "ns123",
		},
		Spec: v1alpha1.BackstageSpec{
			Database: &v1alpha1.Database{
				EnableLocalDb: pointer.Bool(false),
			},
		},
	}

	testObj := createBackstageTest(bs).withDefaultConfig(true)

	model, err := InitObjects(context.TODO(), bs, testObj.rawConfig, nil, true, false, testObj.scheme)

	assert.NoError(t, err)
	assert.True(t, len(model.RuntimeObjects) > 0)
	assert.Equal(t, "bs-deployment", model.backstageDeployment.Object().GetName())
	assert.Equal(t, "ns123", model.backstageDeployment.Object().GetNamespace())
	assert.Equal(t, 2, len(model.backstageDeployment.Object().GetLabels()))
	//	assert.Equal(t, 1, len(model[0].Object().GetOwnerReferences()))

	bsDeployment := model.backstageDeployment
	assert.NotNil(t, bsDeployment.deployment.Spec.Template.Spec.Containers[0])
	assert.Equal(t, backstageContainerName, bsDeployment.deployment.Spec.Template.Spec.Containers[0].Name)
	//	assert.NotNil(t, bsDeployment.deployment.Spec.Template.Spec.Volumes)

	//	assert.Equal(t, "Backstage", bsDeployment.deployment.OwnerReferences[0].Kind)

	bsService := model.backstageService
	assert.Equal(t, "bs-service", bsService.service.Name)
	assert.True(t, len(bsService.service.Spec.Ports) > 0)

	assert.Equal(t, fmt.Sprintf("backstage-%s", "bs"), bsDeployment.deployment.Spec.Template.ObjectMeta.Labels[backstageAppLabel])
	assert.Equal(t, fmt.Sprintf("backstage-%s", "bs"), bsService.service.Spec.Selector[backstageAppLabel])

}

func TestIfEmptyObjectIsValid(t *testing.T) {

	bs := bsv1alpha1.Backstage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bs",
			Namespace: "ns123",
		},
		Spec: bsv1alpha1.BackstageSpec{
			Database: &bsv1alpha1.Database{
				EnableLocalDb: pointer.Bool(false),
			},
		},
	}

	testObj := createBackstageTest(bs).withDefaultConfig(true)

	assert.False(t, bs.Spec.IsLocalDbEnabled())

	model, err := InitObjects(context.TODO(), bs, testObj.rawConfig, nil, true, false, testObj.scheme)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(model.RuntimeObjects))

}

func TestAddToModel(t *testing.T) {

	bs := v1alpha1.Backstage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bs",
			Namespace: "ns123",
		},
		Spec: v1alpha1.BackstageSpec{
			Database: &v1alpha1.Database{
				EnableLocalDb: pointer.Bool(false),
			},
		},
	}
	testObj := createBackstageTest(bs).withDefaultConfig(true)

	model, err := InitObjects(context.TODO(), bs, testObj.rawConfig, nil, true, false, testObj.scheme)
	assert.NoError(t, err)
	assert.NotNil(t, model)
	assert.NotNil(t, model.RuntimeObjects)
	assert.Equal(t, 2, len(model.RuntimeObjects))

	found := false
	for _, bd := range model.RuntimeObjects {
		if bd, ok := bd.(*BackstageDeployment); ok {
			found = true
			assert.Equal(t, bd, model.backstageDeployment)
		}
	}
	assert.True(t, found)

	// another empty model to test
	rm := BackstageModel{RuntimeObjects: []RuntimeObject{}}
	assert.Equal(t, 0, len(rm.RuntimeObjects))
	testService := *model.backstageService

	// add to rm
	testService.addToModel(&rm, bs, true)
	assert.Equal(t, 1, len(rm.RuntimeObjects))
	assert.NotNil(t, rm.backstageService)
	assert.Nil(t, rm.backstageDeployment)
	assert.Equal(t, testService, *rm.backstageService)
	assert.Equal(t, testService, *rm.RuntimeObjects[0].(*BackstageService))
}
