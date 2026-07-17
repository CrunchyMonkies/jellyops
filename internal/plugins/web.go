/*
Copyright 2026.

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

package plugins

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// DefaultWebPort is the port the web-tier nginx container listens on.
const DefaultWebPort int32 = 80

// WebDeploymentName returns the Deployment name for the web tier.
func WebDeploymentName(jf *jellyfinv1alpha1.Jellyfin) string {
	return truncateName(jf.Name + "-web")
}

// WebServiceName returns the Service name for the web tier.
func WebServiceName(jf *jellyfinv1alpha1.Jellyfin) string {
	return truncateName(jf.Name + "-web")
}

// WebLabels are applied to web-tier owned objects.
func WebLabels(jf *jellyfinv1alpha1.Jellyfin) map[string]string {
	return map[string]string{
		NameLabel:      WebAppName,
		InstanceLabel:  jf.Name,
		ManagedByLabel: ManagedByValue,
		ComponentLabel: "web",
	}
}

// WebSelectorLabels are the immutable Deployment/Service selector labels for the
// web tier. The distinct NameLabel (WebAppName) keeps web pods out of the instance
// selectors used by the server Service and plugin gRPC services — a shared NameLabel
// with only an extra component label would still be matched by those subset selectors.
func WebSelectorLabels(jf *jellyfinv1alpha1.Jellyfin) map[string]string {
	return map[string]string{
		NameLabel:      WebAppName,
		InstanceLabel:  jf.Name,
		ComponentLabel: "web",
	}
}

// BuildWebDeployment builds the desired web-tier Deployment for an instance.
func BuildWebDeployment(jf *jellyfinv1alpha1.Jellyfin) *appsv1.Deployment {
	web := jf.Spec.Web
	replicas := int32(1)
	if web.Replicas != nil {
		replicas = *web.Replicas
	}

	container := corev1.Container{
		Name:  "web",
		Image: web.Image,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: DefaultWebPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Resources: web.Resources,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt32(DefaultWebPort),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt32(DefaultWebPort),
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       20,
		},
	}

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		SecurityContext: &corev1.PodSecurityContext{
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	applyPodSecurity(&podSpec.Containers[0], &podSpec, jf.Spec.PodSecurity)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WebDeploymentName(jf),
			Namespace: jf.Namespace,
			Labels:    WebLabels(jf),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: WebSelectorLabels(jf)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      WebSelectorLabels(jf),
					Annotations: web.PodAnnotations,
				},
				Spec: podSpec,
			},
		},
	}
}

// BuildWebService builds the ClusterIP Service for the web-tier Deployment.
func BuildWebService(jf *jellyfinv1alpha1.Jellyfin) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WebServiceName(jf),
			Namespace: jf.Namespace,
			Labels:    WebLabels(jf),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: WebSelectorLabels(jf),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       DefaultWebPort,
				TargetPort: intstr.FromInt32(DefaultWebPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}
