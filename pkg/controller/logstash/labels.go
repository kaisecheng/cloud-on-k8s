// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/labels"
)

const (
	// TypeLabelValue represents the Logstash type.
	TypeLabelValue = "logstash"

	// NameLabelName used to represent an Logstash in k8s resources
	NameLabelName = "logstash.k8s.elastic.co/name"

	// NamespaceLabelName used to represent an Logstash in k8s resources
	NamespaceLabelName = "logstash.k8s.elastic.co/namespace"
)

// NewLabels returns the set of common labels for an Elastic Logstash.
func NewLabels(logstash logstashv1alpha1.Logstash) map[string]string {
	return map[string]string{
		labels.TypeLabelName: TypeLabelValue,
		NameLabelName:        logstash.Name,
	}
}
