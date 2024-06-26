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
	"testing"

	"k8s.io/utils/ptr"

	bsv1alpha1 "redhat-developer/red-hat-developer-hub-operator/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

var dbSecretBackstage = &bsv1alpha1.Backstage{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "bs",
		Namespace: "ns123",
	},
	Spec: bsv1alpha1.BackstageSpec{
		Database: &bsv1alpha1.Database{
			EnableLocalDb: ptr.To(false),
		},
	},
}

func TestEmptyDbSecret(t *testing.T) {

	bs := *dbSecretBackstage.DeepCopy()

	// expected generatePassword = false (default db-secret defined) will come from preprocess
	testObj := createBackstageTest(bs).withDefaultConfig(true).withLocalDb().addToDefaultConfig("db-secret.yaml", "db-empty-secret.yaml")

	model, err := InitObjects(context.TODO(), bs, testObj.externalConfig, true, false, testObj.scheme)

	assert.NoError(t, err)
	assert.NotNil(t, model.LocalDbSecret)
	assert.Equal(t, DbSecretDefaultName(bs.Name), model.LocalDbSecret.secret.Name)

	dbss := model.localDbStatefulSet
	assert.NotNil(t, dbss)
	assert.Equal(t, 1, len(dbss.container().EnvFrom))

	assert.Equal(t, model.LocalDbSecret.secret.Name, dbss.container().EnvFrom[0].SecretRef.Name)
}

func TestDefaultWithGeneratedSecrets(t *testing.T) {
	bs := *dbSecretBackstage.DeepCopy()

	// expected generatePassword = true (no db-secret defined) will come from preprocess
	testObj := createBackstageTest(bs).withDefaultConfig(true).withLocalDb().addToDefaultConfig("db-secret.yaml", "db-generated-secret.yaml")

	model, err := InitObjects(context.TODO(), bs, testObj.externalConfig, true, false, testObj.scheme)

	assert.NoError(t, err)
	assert.Equal(t, DbSecretDefaultName(bs.Name), model.LocalDbSecret.secret.Name)
	//should be generated
	//	assert.NotEmpty(t, model.LocalDbSecret.secret.StringData["POSTGRES_USER"])
	//	assert.NotEmpty(t, model.LocalDbSecret.secret.StringData["POSTGRES_PASSWORD"])

	dbss := model.localDbStatefulSet
	assert.NotNil(t, dbss)
	assert.Equal(t, 1, len(dbss.container().EnvFrom))
	assert.Equal(t, model.LocalDbSecret.secret.Name, dbss.container().EnvFrom[0].SecretRef.Name)
}

//func TestSpecifiedSecret(t *testing.T) {
//	bs := *dbSecretBackstage.DeepCopy()
//	bs.Spec.Database.AuthSecretName = "custom-db-secret"
//
//	// expected generatePassword = false (db-secret defined in the spec) will come from preprocess
//	testObj := createBackstageTest(bs).withDefaultConfig(true).withLocalDb().addToDefaultConfig("db-secret.yaml", "db-generated-secret.yaml")
//
//	model, err := InitObjects(context.TODO(), bs, testObj.detailedSpec, true, false, testObj.scheme)
//
//	assert.NoError(t, err)
//	assert.Equal(t, "custom-db-secret", model.LocalDbSecret.secret.Name)
//
//	assert.Equal(t, "postgres", model.LocalDbSecret.secret.StringData["POSTGRES_USER"])
//	assert.NotEmpty(t, model.LocalDbSecret.secret.StringData["POSTGRES_USER"])
//	assert.Equal(t, "postgres", model.LocalDbSecret.secret.StringData["POSTGRES_PASSWORD"])
//	assert.NotEmpty(t, model.LocalDbSecret.secret.StringData["POSTGRES_PASSWORD"])
//	assert.Equal(t, model.LocalDbSecret.secret.Name, model.localDbStatefulSet.container().EnvFrom[0].SecretRef.Name)
//
//}
