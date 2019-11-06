// Copyright 2019 ArgoCD Operator Developers
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package argocd

import (
	"context"
	"fmt"

	argoproj "github.com/jmckind/argocd-operator/pkg/apis/argoproj/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// newDeployment retuns a new Deployment instance for the given ArgoCD.
func newDeployment(cr *argoproj.ArgoCD) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labelsForCluster(cr),
		},
	}
}

// newDeploymentWithName retuns a new Deployment instance for the given ArgoCD using the given name.
func newDeploymentWithName(name string, component string, cr *argoproj.ArgoCD) *appsv1.Deployment {
	deploy := newDeployment(cr)
	deploy.ObjectMeta.Name = name

	deploy.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				ArgoCDKeyName: name,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					ArgoCDKeyName: name,
				},
			},
		},
	}

	lbls := deploy.ObjectMeta.Labels
	lbls[ArgoCDKeyComponent] = component
	deploy.ObjectMeta.Labels = lbls

	return deploy
}

// newDeploymentWithSuffix retuns a new Deployment instance for the given ArgoCD using the given suffix.
func newDeploymentWithSuffix(suffix string, component string, cr *argoproj.ArgoCD) *appsv1.Deployment {
	return newDeploymentWithName(fmt.Sprintf("%s-%s", cr.Name, suffix), component, cr)
}

func (r *ReconcileArgoCD) reconcileApplicationControllerDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("application-controller", "application-controller", cr)
	if r.isObjectFound(cr.Namespace, deploy.Name, deploy) {
		return nil // Deployment found, do nothing
	}

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{
			"argocd-application-controller",
			"--status-processors",
			"20",
			"--operation-processors",
			"10",
		},
		Image:           getArgoContainerImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		Name:            deploy.Name,
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromInt(8082),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 8082,
			},
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromInt(8082),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
	}}

	deploy.Spec.Template.Spec.ServiceAccountName = "argocd-application-controller"

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}

func (r *ReconcileArgoCD) reconcileDeployments(cr *argoproj.ArgoCD) error {
	err := r.reconcileApplicationControllerDeployment(cr)
	if err != nil {
		return err
	}

	err = r.reconcileDexDeployment(cr)
	if err != nil {
		return err
	}

	err = r.reconcileRedisDeployment(cr)
	if err != nil {
		return err
	}

	err = r.reconcileRepoDeployment(cr)
	if err != nil {
		return err
	}

	err = r.reconcileServerDeployment(cr)
	if err != nil {
		return err
	}

	if IsOpenShift() {
		err = r.reconcileGrafanaDeployment(cr)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileArgoCD) reconcileDexDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("dex-server", "dex-server", cr)
	found := r.isObjectFound(cr.Namespace, deploy.Name, deploy)
	if found {
		return nil // Deployment found, do nothing
	}

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{
			"/shared/argocd-util",
			"rundex",
		},
		Image:           "quay.io/dexidp/dex:v2.14.0",
		ImagePullPolicy: corev1.PullAlways,
		Name:            "dex",
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 5556,
			}, {
				ContainerPort: 5557,
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "static-files",
			MountPath: "/shared",
		}},
	}}

	deploy.Spec.Template.Spec.InitContainers = []corev1.Container{{
		Command: []string{
			"cp",
			"/usr/local/bin/argocd-util",
			"/shared",
		},
		Image:           getArgoContainerImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		Name:            "copyutil",
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "static-files",
			MountPath: "/shared",
		}},
	}}

	deploy.Spec.Template.Spec.ServiceAccountName = "argocd-dex-server"
	deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
		Name: "static-files",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}}

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}

func (r *ReconcileArgoCD) reconcileGrafanaDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("grafana", "grafana", cr)
	found := r.isObjectFound(cr.Namespace, deploy.Name, deploy)
	if found {
		return nil // Deployment found, do nothing
	}

	var replicas int32 = 1
	deploy.Spec.Replicas = &replicas

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Image:           getGrafanaContainerImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		Name:            deploy.Name,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 3000,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "grafana-config",
				MountPath: "/etc/grafana",
			}, {
				Name:      "grafana-datasources-config",
				MountPath: "/etc/grafana/provisioning/datasources",
			}, {
				Name:      "grafana-dashboards-config",
				MountPath: "/etc/grafana/provisioning/dashboards",
			}, {
				Name:      "grafana-dashboard-templates",
				MountPath: "/var/lib/grafana/dashboards",
			},
		},
	}}

	deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "grafana-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: nameWithSuffix("grafana-config", cr),
					},
					Items: []corev1.KeyToPath{{
						Key:  "grafana.ini",
						Path: "grafana.ini",
					}},
				},
			},
		}, {
			Name: "grafana-datasources-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: nameWithSuffix("grafana-config", cr),
					},
					Items: []corev1.KeyToPath{{
						Key:  "datasource.yaml",
						Path: "datasource.yaml",
					}},
				},
			},
		}, {
			Name: "grafana-dashboards-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: nameWithSuffix("grafana-config", cr),
					},
					Items: []corev1.KeyToPath{{
						Key:  "provider.yaml",
						Path: "provider.yaml",
					}},
				},
			},
		}, {
			Name: "grafana-dashboard-templates",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: nameWithSuffix("grafana-dashboards", cr),
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}

func (r *ReconcileArgoCD) reconcileRedisDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("redis", "redis", cr)
	found := r.isObjectFound(cr.Namespace, deploy.Name, deploy)
	if found {
		return nil // Deployment found, do nothing
	}

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Args: []string{
			"--save",
			"",
			"--appendonly",
			"no",
		},
		Image:           "redis:5.0.3",
		ImagePullPolicy: corev1.PullAlways,
		Name:            "redis",
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 6379,
			},
		},
	}}

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}

func (r *ReconcileArgoCD) reconcileRepoDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("repo-server", "repo-server", cr)
	found := r.isObjectFound(cr.Namespace, deploy.Name, deploy)
	if found {
		// Deployment found, do nothing
		return nil
	}

	automountToken := false
	deploy.Spec.Template.Spec.AutomountServiceAccountToken = &automountToken

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{
			"argocd-repo-server",
			"--redis",
			"argocd-redis:6379",
		},
		Image:           getArgoContainerImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(8081),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		Name: deploy.Name,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 8081,
			}, {
				ContainerPort: 8084,
			},
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(8081),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ssh-known-hosts",
				MountPath: "/app/config/ssh",
			}, {
				Name:      "tls-certs",
				MountPath: "/app/config/tls",
			},
		},
	}}

	deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "ssh-known-hosts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ArgoCDKnownHostsConfigMapName,
					},
				},
			},
		}, {
			Name: "tls-certs",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ArgoCDTLSCertsConfigMapName,
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}

func (r *ReconcileArgoCD) reconcileServerDeployment(cr *argoproj.ArgoCD) error {
	deploy := newDeploymentWithSuffix("server", "server", cr)
	found := r.isObjectFound(cr.Namespace, deploy.Name, deploy)
	if found {
		return nil // Deployment found, do nothing
	}

	deploy.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{
			"argocd-server",
			"--staticassets",
			"/shared/app",
		},
		Image:           getArgoContainerImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromInt(8080),
				},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       30,
		},
		Name: deploy.Name,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 8080,
			}, {
				ContainerPort: 8083,
			},
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromInt(8080),
				},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       30,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ssh-known-hosts",
				MountPath: "/app/config/ssh",
			}, {
				Name:      "tls-certs",
				MountPath: "/app/config/tls",
			},
		},
	}}

	deploy.Spec.Template.Spec.ServiceAccountName = "argocd-server"

	deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "ssh-known-hosts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ArgoCDKnownHostsConfigMapName,
					},
				},
			},
		}, {
			Name: "tls-certs",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ArgoCDTLSCertsConfigMapName,
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cr, deploy, r.scheme); err != nil {
		return err
	}
	return r.client.Create(context.TODO(), deploy)
}