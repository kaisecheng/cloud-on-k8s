package sset

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	DefaultDataVolumeName = "logstash-data"
	DefaultDataMountPath  = "/usr/share/logstash/data"

	DefaultPersistentVolumeSize = resource.MustParse("1.5Gi")

	// DefaultDataVolumeClaim is the default data volume claim
	DefaultDataVolumeClaim = corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultDataVolumeName,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: DefaultPersistentVolumeSize,
				},
			},
		},
	}
	DefaultDataVolumeMount = corev1.VolumeMount{
		Name:      DefaultDataVolumeName,
		MountPath: DefaultDataMountPath,
	}

	DefaultVolumeClaimTemplates = []corev1.PersistentVolumeClaim{DefaultDataVolumeClaim}
)

func BuildPVCs(pvc []corev1.PersistentVolumeClaim) []corev1.PersistentVolumeClaim {
	for _, p := range pvc {
		if p.Name == DefaultDataVolumeName {
			return pvc
		}
	}

	return append(pvc, DefaultDataVolumeClaim)
}
