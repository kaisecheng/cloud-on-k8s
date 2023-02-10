// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package v1alpha1

import (
	"fmt"
	commonv1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/common/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Kind is inferred from the struct name using reflection in SchemeBuilder.Register()
	// we duplicate it as a constant here for practical purposes.
	Kind = "Logstash"
)

// LogstashSpec defines the desired state of Logstash
type LogstashSpec struct {
	// Version of the Logstash.
	Version string `json:"version"`

	// ElasticsearchRef is a reference to an Elasticsearch cluster running in the same Kubernetes cluster.
	// +kubebuilder:validation:Optional
	ElasticsearchRef commonv1.ObjectSelector `json:"elasticsearchRef,omitempty"`

	// Image is the Logstash Docker image to deploy. Version and Type have to match the Logstash in the image.
	// +kubebuilder:validation:Optional
	Image string `json:"image,omitempty"`

	// Config holds the Logstash configuration. At most one of [`Config`, `ConfigRef`] can be specified.
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Config *commonv1.Config `json:"config,omitempty"`

	// ConfigRef contains a reference to an existing Kubernetes Secret holding the Logstash configuration.
	// Logstash settings must be specified as yaml, under a single "logstash.yml" entry. At most one of [`Config`, `ConfigRef`]
	// can be specified.
	// +kubebuilder:validation:Optional
	ConfigRef *commonv1.ConfigSource `json:"configRef,omitempty"`

	// Pipelines holds the Logstash Pipelines. At most one of [`Pipelines`, `PipelineRef`] can be specified.
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Pipelines []map[string]string `json:"pipelines,omitempty"`

	// PipelineRef contains a reference to an existing Kubernetes Secret holding the Logstash Pipelines.
	// Logstash pipeline must be specified as yaml, under a single "pipeline.yml" entry. At most one of [`Pipelines`, `PipelineRef`]
	// can be specified.
	// +kubebuilder:validation:Optional
	PipelineRef *commonv1.ConfigSource `json:"pipelineRef,omitempty"`

	// SecureSettings is a list of references to Kubernetes Secrets containing sensitive configuration options for the Logstash.
	// Secrets data can be then referenced in the Logstash config using the Secret's keys or as specified in `Entries` field of
	// each SecureSetting.
	// +kubebuilder:validation:Optional
	SecureSettings []commonv1.SecretSource `json:"secureSettings,omitempty"`

	// ServiceAccountName is used to check access from the current resource to Elasticsearch resource in a different namespace.
	// Can only be used if ECK is enforcing RBAC on references.
	// +kubebuilder:validation:Optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Deployment specifies the Logstash should be deployed as a Deployment, and allows providing its spec.
	// Cannot be used along with `StatefulSet`.
	// +kubebuilder:validation:Optional
	Deployment *DeploymentSpec `json:"deployment,omitempty"`

	// StatefulSet specifies the Logstash should be deployed as a StatefulSet, and allows providing its spec.
	// Cannot be used along with `Deployment`.
	// +kubebuilder:validation:Optional
	StatefulSet *StatefulSetSpec `json:"statefulSet,omitempty"`

	// RevisionHistoryLimit is the number of revisions to retain to allow rollback in the underlying DaemonSet or Deployment.
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`

	// Services is a list of Service configuration for Logstash
	// When it is empty, a default Service for port 9600 is created.
	// When it is not empty, the port 9600 does not attach to any of Service.
	// +kubebuilder:validation:Optional
	Services []LogstashService `json:"services,omitempty"`

	// HTTP holds the HTTP layer configuration for Logstash metrics API.
	// +kubebuilder:validation:Optional
	HTTP commonv1.HTTPConfig `json:"http,omitempty"`

	// Monitoring enables you to collect and ship log and monitoring data of this Logstash.
	// See https://www.elastic.co/guide/en/kibana/current/xpack-monitoring.html.
	// Metricbeat and Filebeat are deployed in the same Pod as sidecars and each one sends data to one or two different
	// Elasticsearch monitoring clusters running in the same Kubernetes cluster.
	// +kubebuilder:validation:Optional
	Monitoring commonv1.Monitoring `json:"monitoring,omitempty"`
}

type DeploymentSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate,omitempty"`
	Replicas    *int32                 `json:"replicas,omitempty"`
	// +kubebuilder:validation:Optional
	Strategy appsv1.DeploymentStrategy `json:"strategy,omitempty"`
}

type StatefulSetSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate,omitempty"`
	Replicas    *int32                 `json:"replicas,omitempty"`
	// VolumeClaimTemplates is a list of persistent volume claims to be used by each Pod in this NodeSet.
	// Every claim in this list must have a matching volumeMount in one of the containers defined in the PodTemplate.
	// Items defined here take precedence over any default claims added by the operator with the same name.
	// +kubebuilder:validation:Optional
	VolumeClaimTemplates []corev1.PersistentVolumeClaim `json:"volumeClaimTemplates,omitempty"`
}

// LogstashService defines the service of Logstash
type LogstashService struct {
	Name string `json:"name,omitempty"`
	// Service defines the template for the associated Kubernetes Service
	Service commonv1.ServiceTemplate `json:"service,omitempty"`
}

// LogstashStatus defines the observed state of Logstash
type LogstashStatus struct {
	// Version of the stack resource currently running. During version upgrades, multiple versions may run
	// in parallel: this value specifies the lowest version currently running.
	Version string `json:"version,omitempty"`

	// +kubebuilder:validation:Optional
	ExpectedNodes int32 `json:"expectedNodes,omitempty"`
	// +kubebuilder:validation:Optional
	AvailableNodes int32 `json:"availableNodes,omitempty"`

	// ElasticsearchAssociationStatus is the status of any auto-linking to Elasticsearch clusters.
	ElasticsearchAssociationStatus commonv1.AssociationStatus `json:"elasticsearchAssociationStatus,omitempty"`

	// MonitoringAssociationStatus is the status of any auto-linking to monitoring Elasticsearch clusters.
	MonitoringAssociationStatus commonv1.AssociationStatusMap `json:"monitoringAssociationStatus,omitempty"`

	// ObservedGeneration is the most recent generation observed for this Logstash instance.
	// It corresponds to the metadata generation, which is updated on mutation by the API Server.
	// If the generation observed in status diverges from the generation in metadata, the Logstash
	// controller has not yet processed the changes contained in the Logstash specification.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Logstash is the Schema for the logstashes API
// +k8s:openapi-gen=true
// +kubebuilder:resource:categories=elastic,shortName=logstash
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
type Logstash struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec                 LogstashSpec                                         `json:"spec,omitempty"`
	Status               LogstashStatus                                       `json:"status,omitempty"`
	esAssocConf          *commonv1.AssociationConf                            `json:"-"`
	monitoringAssocConfs map[commonv1.ObjectSelector]commonv1.AssociationConf `json:"-"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LogstashList contains a list of Logstash
type LogstashList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Logstash `json:"items"`
}

type LogstashESAssociation struct {
	*Logstash
}

var _ commonv1.Associated = &Logstash{}

func (l *Logstash) ServiceAccountName() string {
	return l.Spec.ServiceAccountName
}

func (l *Logstash) GetAssociations() []commonv1.Association {
	associations := make([]commonv1.Association, 0)

	if l.Spec.ElasticsearchRef.IsDefined() {
		associations = append(associations, &LogstashESAssociation{
			Logstash: l,
		})
	}

	for _, ref := range l.Spec.Monitoring.Metrics.ElasticsearchRefs {
		if ref.IsDefined() {
			associations = append(associations, &LogstashMonitoringAssociation{
				Logstash: l,
				ref:      ref.WithDefaultNamespace(l.Namespace),
			})
		}
	}
	for _, ref := range l.Spec.Monitoring.Logs.ElasticsearchRefs {
		if ref.IsDefined() {
			associations = append(associations, &LogstashMonitoringAssociation{
				Logstash: l,
				ref:      ref.WithDefaultNamespace(l.Namespace),
			})
		}
	}

	return associations
}

func (l *Logstash) AssociationStatusMap(typ commonv1.AssociationType) commonv1.AssociationStatusMap {
	switch typ {
	case commonv1.ElasticsearchAssociationType:
		if l.Spec.ElasticsearchRef.IsDefined() {
			return commonv1.NewSingleAssociationStatusMap(l.Status.ElasticsearchAssociationStatus)
		}
	case commonv1.LogstashMonitoringAssociationType:
		for _, esRef := range l.Spec.Monitoring.Metrics.ElasticsearchRefs {
			if esRef.IsDefined() {
				return l.Status.MonitoringAssociationStatus
			}
		}
		for _, esRef := range l.Spec.Monitoring.Logs.ElasticsearchRefs {
			if esRef.IsDefined() {
				return l.Status.MonitoringAssociationStatus
			}
		}
	}

	return commonv1.AssociationStatusMap{}
}

func (l *Logstash) SetAssociationStatusMap(typ commonv1.AssociationType, status commonv1.AssociationStatusMap) error {
	single, err := status.Single()
	if err != nil {
		return err
	}

	switch typ {
	case commonv1.ElasticsearchAssociationType:
		l.Status.ElasticsearchAssociationStatus = single
		return nil
	case commonv1.LogstashMonitoringAssociationType:
		l.Status.MonitoringAssociationStatus = status
		return nil
	default:
		return fmt.Errorf("association type %s not known", typ)
	}
}

var _ commonv1.Association = &LogstashESAssociation{}

func (la *LogstashESAssociation) ElasticServiceAccount() (commonv1.ServiceAccountName, error) {
	return "", nil
}

func (la *LogstashESAssociation) Associated() commonv1.Associated {
	if la == nil {
		return nil
	}
	if la.Logstash == nil {
		la.Logstash = &Logstash{}
	}
	return la.Logstash
}

func (la *LogstashESAssociation) AssociationType() commonv1.AssociationType {
	return commonv1.ElasticsearchAssociationType
}

func (la *LogstashESAssociation) AssociationRef() commonv1.ObjectSelector {
	return la.Spec.ElasticsearchRef.WithDefaultNamespace(la.Namespace)
}

func (la *LogstashESAssociation) AssociationConfAnnotationName() string {
	return commonv1.ElasticsearchConfigAnnotationNameBase
}

func (la *LogstashESAssociation) AssociationConf() (*commonv1.AssociationConf, error) {
	return commonv1.GetAndSetAssociationConf(la, la.esAssocConf)
}

func (la *LogstashESAssociation) SetAssociationConf(conf *commonv1.AssociationConf) {
	la.esAssocConf = conf
}

func (la *LogstashESAssociation) AssociationID() string {
	return commonv1.SingletonAssociationID
}

func (l *Logstash) SecureSettings() []commonv1.SecretSource {
	return l.Spec.SecureSettings
}

// IsMarkedForDeletion returns true if the Logstash is going to be deleted
func (l *Logstash) IsMarkedForDeletion() bool {
	return !l.DeletionTimestamp.IsZero()
}

func (l *Logstash) ElasticsearchRef() commonv1.ObjectSelector {
	return l.Spec.ElasticsearchRef
}

// GetObservedGeneration will return the observedGeneration from the Elastic Logstash's status.
func (l *Logstash) GetObservedGeneration() int64 {
	return l.Status.ObservedGeneration
}

type LogstashMonitoringAssociation struct {
	// The associated Logstash
	*Logstash
	// ref is the object selector of the monitoring Elasticsearch referenced in the Association
	ref commonv1.ObjectSelector
}

var _ commonv1.Association = &LogstashMonitoringAssociation{}

func (lsmon *LogstashMonitoringAssociation) ElasticServiceAccount() (commonv1.ServiceAccountName, error) {
	return "", nil
}

func (lsmon *LogstashMonitoringAssociation) Associated() commonv1.Associated {
	if lsmon == nil {
		return nil
	}
	if lsmon.Logstash == nil {
		lsmon.Logstash = &Logstash{}
	}
	return lsmon.Logstash
}

func (lsmon *LogstashMonitoringAssociation) AssociationConfAnnotationName() string {
	return commonv1.ElasticsearchConfigAnnotationName(lsmon.ref)
}

func (lsmon *LogstashMonitoringAssociation) AssociationType() commonv1.AssociationType {
	return commonv1.LogstashMonitoringAssociationType
}

func (lsmon *LogstashMonitoringAssociation) AssociationRef() commonv1.ObjectSelector {
	return lsmon.ref
}

func (lsmon *LogstashMonitoringAssociation) AssociationConf() (*commonv1.AssociationConf, error) {
	return commonv1.GetAndSetAssociationConfByRef(lsmon, lsmon.ref, lsmon.monitoringAssocConfs)
}

func (lsmon *LogstashMonitoringAssociation) SetAssociationConf(assocConf *commonv1.AssociationConf) {
	if lsmon.monitoringAssocConfs == nil {
		lsmon.monitoringAssocConfs = make(map[commonv1.ObjectSelector]commonv1.AssociationConf)
	}
	if assocConf != nil {
		lsmon.monitoringAssocConfs[lsmon.ref] = *assocConf
	}
}

func (lsmon *LogstashMonitoringAssociation) AssociationID() string {
	return lsmon.ref.ToID()
}

func (l *Logstash) GetMonitoringMetricsRefs() []commonv1.ObjectSelector {
	return l.Spec.Monitoring.Metrics.ElasticsearchRefs
}

func (l *Logstash) GetMonitoringLogsRefs() []commonv1.ObjectSelector {
	return l.Spec.Monitoring.Logs.ElasticsearchRefs
}

func (l *Logstash) MonitoringAssociation(esRef commonv1.ObjectSelector) commonv1.Association {
	return &LogstashMonitoringAssociation{
		Logstash: l,
		ref:      esRef.WithDefaultNamespace(l.Namespace),
	}
}

func init() {
	SchemeBuilder.Register(&Logstash{}, &LogstashList{})
}
