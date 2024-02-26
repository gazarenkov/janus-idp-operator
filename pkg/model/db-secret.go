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
	"strconv"

	bsv1alpha1 "janus-idp.io/backstage-operator/api/v1alpha1"
	"janus-idp.io/backstage-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DbSecretFactory struct{}

func (f DbSecretFactory) newBackstageObject() RuntimeObject {
	return &DbSecret{}
}

type DbSecret struct {
	secret *corev1.Secret
}

func init() {
	registerConfig("db-secret.yaml", DbSecretFactory{})
}

func DbSecretDefaultName(backstageName string) string {
	return utils.GenerateRuntimeObjectName(backstageName, "default-dbsecret")
}

// implementation of RuntimeObject interface
func (b *DbSecret) Object() client.Object {
	return b.secret
}

func (b *DbSecret) setObject(obj client.Object, backstageName string) {
	b.secret = nil
	if obj != nil {
		b.secret = obj.(*corev1.Secret)
	}
}

// implementation of RuntimeObject interface
func (b *DbSecret) addToModel(model *BackstageModel, backstage bsv1alpha1.Backstage, ownsRuntime bool) (bool, error) {

	if b.secret != nil && model.localDbEnabled {
		model.setRuntimeObject(b)
		model.LocalDbSecret = b
		return true, nil
	}
	return false, nil
}

// implementation of RuntimeObject interface
func (b *DbSecret) EmptyObject() client.Object {
	return &corev1.Secret{}
}

// implementation of RuntimeObject interface
func (b *DbSecret) validate(model *BackstageModel, backstage bsv1alpha1.Backstage) error {

	if backstage.Spec.IsAuthSecretSpecified() || !backstage.Spec.IsLocalDbEnabled() {
		return nil
	}

	pswd, _ := utils.GeneratePassword(24)
	service := model.LocalDbService

	b.secret.StringData = map[string]string{
		"POSTGRES_PASSWORD":         pswd,
		"POSTGRESQL_ADMIN_PASSWORD": pswd,
		"POSTGRES_USER":             "postgres",
		"POSTGRES_HOST":             service.service.GetName(),
		"POSTGRES_PORT":             strconv.FormatInt(int64(service.service.Spec.Ports[0].Port), 10),
	}

	return nil
}

func (b *DbSecret) setMetaInfo(backstageName string) {
	b.secret.SetName(DbSecretDefaultName(backstageName))
}
