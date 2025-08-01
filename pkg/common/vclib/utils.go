/*
Copyright 2018 The Kubernetes Authors.

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

package vclib

import (
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// IsNotFound return true if err is NotFoundError or DefaultNotFoundError
func IsNotFound(err error) bool {
	_, ok := err.(*find.NotFoundError)
	if ok {
		return true
	}

	_, ok = err.(*find.DefaultNotFoundError)
	return ok
}

func getFinder(dc *Datacenter) *find.Finder {
	finder := find.NewFinder(dc.Client(), false)
	finder.SetDatacenter(dc.Datacenter)
	return finder
}

// IsManagedObjectNotFoundError returns true if error is of type ManagedObjectNotFound
func IsManagedObjectNotFoundError(err error) bool {
	isManagedObjectNotFoundError := false
	if soap.IsSoapFault(err) {
		_, isManagedObjectNotFoundError = soap.ToSoapFault(err).VimFault().(types.ManagedObjectNotFound)
	}
	return isManagedObjectNotFoundError
}

// IsInvalidCredentialsError returns true if error is of type InvalidLogin
func IsInvalidCredentialsError(err error) bool {
	isInvalidCredentialsError := false
	if soap.IsSoapFault(err) {
		_, isInvalidCredentialsError = soap.ToSoapFault(err).VimFault().(types.InvalidLogin)
	}
	return isInvalidCredentialsError
}
