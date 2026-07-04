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

package controller

import (
	"context"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// writeJellyfinStatus persists jf.Status, retrying on optimistic-concurrency
// conflicts. Two controllers (JellyfinReconciler and JellyfinAPIReconciler)
// write the same Jellyfin object's status, so a plain Status().Update races; on
// conflict we re-fetch the latest object and re-apply the computed status.
func writeJellyfinStatus(ctx context.Context, c client.Client, jf *jellyfinv1alpha1.Jellyfin) error {
	desired := jf.Status
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur jellyfinv1alpha1.Jellyfin
		if err := c.Get(ctx, client.ObjectKeyFromObject(jf), &cur); err != nil {
			return err
		}
		cur.Status = desired
		return c.Status().Update(ctx, &cur)
	})
}
