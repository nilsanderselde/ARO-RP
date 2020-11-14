package dynamichelper

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func SetControllerReferences(resources []runtime.Object, owner metav1.Object) error {
	for _, resource := range resources {
		r, err := meta.Accessor(resource)
		if err != nil {
			return err
		}

		err = controllerutil.SetControllerReference(owner, r, scheme.Scheme)
		if err != nil {
			return err
		}
	}

	return nil
}

func Prepare(resources []runtime.Object) ([]*unstructured.Unstructured, error) {
	err := hashWorkloadConfigs(resources)
	if err != nil {
		return nil, err
	}

	uns := make([]*unstructured.Unstructured, 0, len(resources))
	for _, resource := range resources {
		un := &unstructured.Unstructured{}
		err = scheme.Scheme.Convert(resource, un, nil)
		if err != nil {
			return nil, err
		}
		uns = append(uns, un)
	}

	sort.Slice(uns, func(i, j int) bool {
		return createOrder(uns[i], uns[j])
	})

	return uns, nil
}

func addWorkloadHashes(o *metav1.ObjectMeta, t *v1.PodTemplateSpec, configToHash map[string]string) {
	for _, v := range t.Spec.Volumes {
		if v.Secret != nil {
			if hash, found := configToHash[keyFunc(schema.GroupKind{Kind: "Secret"}, o.Namespace, v.Secret.SecretName)]; found {
				if t.Annotations == nil {
					t.Annotations = map[string]string{}
				}
				t.Annotations["checksum/secret-"+v.Secret.SecretName] = hash
			}
		}

		if v.ConfigMap != nil {
			if hash, found := configToHash[keyFunc(schema.GroupKind{Kind: "ConfigMap"}, o.Namespace, v.ConfigMap.Name)]; found {
				if t.Annotations == nil {
					t.Annotations = map[string]string{}
				}
				t.Annotations["checksum/configmap-"+v.ConfigMap.Name] = hash
			}
		}
	}
}

// hashWorkloadConfigs iterates daemonsets, walks their volumes, and updates
// their pod templates with annotations that include the hashes of the content
// for each configmap or secret.
func hashWorkloadConfigs(resources []runtime.Object) error {
	// map config resources to their hashed content
	configToHash := map[string]string{}
	for _, o := range resources {
		switch o := o.(type) {
		case *v1.Secret:
			configToHash[keyFunc(schema.GroupKind{Kind: "Secret"}, o.Namespace, o.Name)] = getHashSecret(o)
		case *v1.ConfigMap:
			configToHash[keyFunc(schema.GroupKind{Kind: "ConfigMap"}, o.Namespace, o.Name)] = getHashConfigMap(o)
		}
	}

	// iterate over workload controllers and add annotations with the hashes of
	// every config map or secret appropriately to force redeployments on config
	// updates.
	for _, o := range resources {
		switch o := o.(type) {
		case *appsv1.DaemonSet:
			addWorkloadHashes(&o.ObjectMeta, &o.Spec.Template, configToHash)

		case *appsv1.Deployment:
			addWorkloadHashes(&o.ObjectMeta, &o.Spec.Template, configToHash)

		case *appsv1.StatefulSet:
			addWorkloadHashes(&o.ObjectMeta, &o.Spec.Template, configToHash)
		}
	}

	return nil
}

func getHashSecret(o *v1.Secret) string {
	keys := make([]string, 0, len(o.Data))
	for key := range o.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(h, "%s: %s\n", key, string(o.Data[key]))
	}

	return hex.EncodeToString(h.Sum(nil))
}

func getHashConfigMap(o *v1.ConfigMap) string {
	keys := make([]string, 0, len(o.Data))
	for key := range o.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(h, "%s: %s\n", key, o.Data[key])
	}

	return hex.EncodeToString(h.Sum(nil))
}
