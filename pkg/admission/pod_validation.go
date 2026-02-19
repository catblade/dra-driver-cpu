/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use it except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package admission

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ClaimCPUCountGetter returns the total CPU count for a ResourceClaim by name.
// Used by ValidatePodClaims to resolve claim references without depending on a Kubernetes client.
type ClaimCPUCountGetter interface {
	ClaimCPUCount(ctx context.Context, namespace, claimName string) (int64, error)
}

// ValidatePodClaims enforces that container CPU requests match the sum of CPUs from
// referenced dra.cpu ResourceClaims. It returns a list of human-readable error strings.
func ValidatePodClaims(ctx context.Context, pod *corev1.Pod, driverName string, getter ClaimCPUCountGetter) []string {
	if pod == nil || len(pod.Spec.ResourceClaims) == 0 {
		return nil
	}

	claimNameToResource := make(map[string]string)
	for _, rc := range pod.Spec.ResourceClaims {
		if rc.Name == "" || rc.ResourceClaimName == nil {
			continue
		}
		claimNameToResource[rc.Name] = *rc.ResourceClaimName
	}

	if len(claimNameToResource) == 0 {
		return nil
	}

	var errs []string
	for _, container := range pod.Spec.Containers {
		cpuRequestValue := int64(0)
		cpuQuantity, cpuSpecified := container.Resources.Requests[corev1.ResourceCPU]
		if cpuSpecified {
			cpuRequestValue = CPURequestCount(cpuQuantity)
		}

		totalClaimCPUs := int64(0)
		for _, claim := range container.Resources.Claims {
			resourceClaimName, ok := claimNameToResource[claim.Name]
			if !ok {
				continue
			}
			claimCPUs, err := getter.ClaimCPUCount(ctx, pod.Namespace, resourceClaimName)
			if err != nil {
				errs = append(errs, fmt.Sprintf("container %q: failed to get ResourceClaim %q: %v", container.Name, resourceClaimName, err))
				continue
			}
			totalClaimCPUs += claimCPUs
		}

		if !cpuSpecified && totalClaimCPUs == 0 {
			continue
		}
		if totalClaimCPUs > 0 && !cpuSpecified {
			continue
		}
		if totalClaimCPUs > 0 && cpuRequestValue != totalClaimCPUs {
			errs = append(errs, fmt.Sprintf("container %q: expected %d CPU cores from dra.cpu claims, got %d in cpu requests", container.Name, totalClaimCPUs, cpuRequestValue))
		}
	}

	return errs
}

// CPURequestCount normalizes a CPU quantity to integer cores for validation.
// Fractional or invalid values round up to 1.
func CPURequestCount(cpuQuantity resource.Quantity) int64 {
	value, ok := cpuQuantity.AsInt64()
	if !ok {
		return 1
	}
	if value < 1 {
		return 1
	}
	intQuantity := resource.NewQuantity(value, cpuQuantity.Format)
	if cpuQuantity.Cmp(*intQuantity) != 0 {
		return 1
	}
	return value
}
