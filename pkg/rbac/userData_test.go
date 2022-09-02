package rbac

import (
	"context"
	"sync"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	authz "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	testingk8s "k8s.io/client-go/testing"
)

// Initialize cache object to use tests.
func mockNamespaceCache() *Cache {

	return &Cache{
		users:            map[string]*UserDataCache{},
		shared:           SharedData{},
		restConfig:       &rest.Config{},
		tokenReviews:     map[string]*tokenReviewCache{},
		tokenReviewsLock: sync.Mutex{},
	}
}

func setupToken(cache *Cache) *Cache {
	cache.tokenReviews["123456"] = &tokenReviewCache{
		updatedAt: time.Now(),
		tokenReview: &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				User: authv1.UserInfo{
					UID: "unique-user-id",
				},
			},
		},
	}

	return cache
}

func addCSResources(cache *Cache, res []Resource) *Cache {
	cache.shared.csResources = append(cache.shared.csResources, res...)
	return cache
}

func Test_getNamespaces_emptyCache(t *testing.T) {
	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)

	var namespaces []string

	mock_cache.shared.namespaces = append(namespaces, "some-namespace")
	// mock_cache.shared.managedClusters = append(managedclusters, "some-namespace", "some-nonmatching-namespace")

	rulesCheck := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-namespace",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{"k8s.io"},
					Resources: []string{"nodes1", "nodes2"},
				},
			},
		},
	}
	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		_, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectRulesReview")
			return
		}
		return true, rulesCheck, nil
	})
	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.NsResources) != 1 ||
		result.userData.NsResources["some-namespace"][0].Apigroup != "k8s.io" ||
		result.userData.NsResources["some-namespace"][1].Apigroup != "k8s.io" ||
		result.userData.NsResources["some-namespace"][0].Kind != "nodes1" ||
		result.userData.NsResources["some-namespace"][1].Kind != "nodes2" {
		t.Errorf("Cache does not have expected namespace resources ")

	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

}

func Test_getNamespaces_usingCache(t *testing.T) {
	var namespaces, managedclusters []string
	nsresources := make(map[string][]Resource)

	mock_cache := mockNamespaceCache()
	//mock cache for token review to get user data:
	mock_cache = setupToken(mock_cache)

	//mock cache for cluster-scoped resouces to get all namespaces:
	mock_cache.shared.namespaces = append(namespaces, "some-namespace")
	//mock cache for managed clusters
	mock_cache.shared.managedClusters = append(managedclusters, "some-namespace", "some-nonmatching-namespace")

	//mock cache for namespaced-resources:
	nsresources["some-namespace"] = append(nsresources["some-namespace"],
		Resource{Apigroup: "some-apigroup", Kind: "some-kind"})

	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData: UserData{ManagedClusters: managedclusters,
			NsResources: nsresources},
		csrUpdatedAt:      time.Now(),
		nsrUpdatedAt:      time.Now(),
		clustersUpdatedAt: time.Now(),
	}

	rulesCheck := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-namespace",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{"k8s.io"},
					Resources: []string{"nodes1", "nodes2"},
				},
			},
		},
	}
	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		_, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectRulesReview")
			return
		}
		return true, rulesCheck, nil
	})

	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.NsResources) != 1 ||
		result.userData.NsResources["some-nonmatching-namespace"] != nil ||
		result.userData.NsResources["some-namespace"][0].Apigroup != "some-apigroup" ||
		result.userData.NsResources["some-namespace"][0].Kind != "some-kind" {
		t.Error("Resources not in cache.")
	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

}

func Test_getNamespaces_expiredCache(t *testing.T) {

	var namespaces, managedclusters []string
	nsresources := make(map[string][]Resource)

	mock_cache := mockNamespaceCache()

	//mock cache for token review to get user data:
	mock_cache = setupToken(mock_cache)

	//mock cache for cluster-scoped resouces to get all namespaces:
	mock_cache.shared.namespaces = append(namespaces, "some-namespace")
	mock_cache.shared.managedClusters = append(managedclusters, "some-namespace", "some-nonmatching-namespace")

	//mock cache for namespaced-resources:
	nsresources["some-namespace"] = append(nsresources["some-namespace"],
		Resource{Apigroup: "some-apigroup", Kind: "some-kind"})

	last_cache_time := time.Now().Add(time.Duration(-5) * time.Minute)
	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData:     UserData{NsResources: nsresources},
		nsrUpdatedAt: last_cache_time,
	}
	rulesCheck := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-namespace",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{"k8s.io"},
					Resources: []string{"nodes1", "nodes2"},
				},
			},
		},
	}
	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		_, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectRulesReview")
			return
		}
		return true, rulesCheck, nil
	})
	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.NsResources) != 1 || len(result.userData.NsResources["some-namespace"]) != 2 ||
		result.userData.NsResources["some-nonmatching-namespace"] != nil ||
		result.userData.NsResources["some-namespace"][0].Apigroup != "k8s.io" ||
		result.userData.NsResources["some-namespace"][1].Apigroup != "k8s.io" ||
		result.userData.NsResources["some-namespace"][0].Kind != "nodes1" ||
		result.userData.NsResources["some-namespace"][1].Kind != "nodes2" {
		t.Errorf("Cache does not have expected namespace resources ")

	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

	//  Verify that cache was updated by checking the timestamp
	if !mock_cache.users["unique-user-id"].nsrUpdatedAt.After(last_cache_time) {
		t.Error("Expected the cache.users.updatedAt to have a later timestamp")
	}
}

func Test_clusterScoped_usingCache(t *testing.T) {

	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)
	var managedClusters []string

	res := []Resource{{Apigroup: "storage.k8s.io", Kind: "nodes"}}
	mock_cache = addCSResources(mock_cache, res)

	//mock cache for cluster-scoped resouces

	allowedres := []Resource{{Apigroup: "storage.k8s.io", Kind: "nodes"}}
	managedClusters = append(managedClusters, "some-namespace")

	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData: UserData{CsResources: allowedres,
			ManagedClusters: managedClusters},
		clustersUpdatedAt: time.Now(),
		// Using current time , GetUserData should have the same values as cache
		csrUpdatedAt: time.Now(),
		nsrUpdatedAt: time.Now(),
	}
	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")

	result, err := mock_cache.GetUserData(ctx, nil)
	if len(result.userData.CsResources) != 1 || result.userData.CsResources[0].Kind != "nodes" || result.userData.CsResources[0].Apigroup != "storage.k8s.io" {
		t.Error("Cluster scoped Resources not in user cache.")
	}
	if err != nil {
		t.Error("Unexpected error while obtaining cluster scoped resources.", err)
	}

}
func Test_clusterScoped_expiredCache(t *testing.T) {

	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)

	//mock response objects for KUBEAPI call for SelfSubjectAccessReview
	trueCheck := &authz.SelfSubjectAccessReview{
		Spec: authz.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authz.ResourceAttributes{
				Verb:     "list",
				Group:    "storage.k8s.io",
				Resource: "nodes",
			},
		},
		Status: authz.SubjectAccessReviewStatus{
			Allowed: true,
		},
	}

	falseCheck := &authz.SelfSubjectAccessReview{
		Spec: authz.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authz.ResourceAttributes{
				Verb:     "list",
				Group:    "k8s.io",
				Resource: "csinodes",
			},
		},
		Status: authz.SubjectAccessReviewStatus{
			Allowed: false,
		},
	}

	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		meta, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectAccessReview")
			return
		}
		testSSAR := meta.(*authz.SelfSubjectAccessReview)
		// Mimic user has Authorization to list nodes
		if testSSAR.Spec.ResourceAttributes.Resource == "nodes" {
			return true, trueCheck, nil
		}
		// Mimic user has no Authorization to list other types
		return true, falseCheck, nil
	})

	res := []Resource{{Apigroup: "storage.k8s.io", Kind: "nodes"}, {Apigroup: "k8s.io", Kind: "csinodes"}}
	mock_cache = addCSResources(mock_cache, res)
	// Setup a allowed resource , we have have access to this resource through falseCheck Object
	// So when the next GetUserData executes user should not have this resource in allowed list
	allowedres := []Resource{{Apigroup: "k8s.io", Kind: "csinodes"}}
	last_cache_time := time.Now().Add(time.Duration(-5) * time.Minute)
	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData:     UserData{CsResources: allowedres},
		csrUpdatedAt: last_cache_time,
		authzClient:  fs.AuthorizationV1(),
	}

	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.CsResources) != 1 || result.userData.CsResources[0].Kind != "nodes" {
		t.Error("Cluster scoped Resources not in user cache.")
		t.Errorf("Resource count present in cache %d", len(result.userData.CsResources))
	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

	//  Verify that cache was updated by checking the timestamp
	if !mock_cache.users["unique-user-id"].csrUpdatedAt.After(last_cache_time) {
		t.Error("Expected the cache.users.updatedAt to have a later timestamp")
	}

}
func Test_managedClusters_emptyCache(t *testing.T) {

	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)

	var sharedmanagedclusters, namespaces []string
	mock_cache.shared.managedClusters = append(sharedmanagedclusters, "some-managed-cluster", "some-managed-cluster1")
	mock_cache.shared.namespaces = append(namespaces, "some-managed-cluster", "some-managed-cluster1")

	//mock response objects for KUBEAPI call for SelfSubjectRulesReview
	createRule := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-managed-cluster",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{"apigroup"},
					Resources: []string{"managedclusterviews"},
				},
			},
		},
	}
	notCreateRule := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-managed-cluster1",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{"apigroup"},
					Resources: []string{"managedclusterviews"},
				},
			},
		},
	}

	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		meta, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectRulesReview")
			return
		}
		testSSRR := meta.(*authz.SelfSubjectRulesReview)
		//	Mimic user has Authorization to create managedclusterview
		if testSSRR.Spec.Namespace == "some-managed-cluster" {
			return true, createRule, nil
		}

		// // Mimic user has no Authorization to create managedclusterview
		return true, notCreateRule, nil

	})
	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.ManagedClusters) != 1 || result.userData.ManagedClusters[0] != "some-managed-cluster" {
		t.Errorf("Managed cluster count present in cache %d", len(result.userData.ManagedClusters))
	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

}

func Test_managedClusters_usingCache(t *testing.T) {

	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)
	var managedClusters []string

	res := []Resource{{Apigroup: "storage.k8s.io", Kind: "nodes"}}
	mock_cache = addCSResources(mock_cache, res)

	//mock cache for cluster-scoped resouces

	allowedres := []Resource{{Apigroup: "storage.k8s.io", Kind: "nodes"}}
	managedClusters = append(managedClusters, "some-managed-cluster", "some-other-managed-cluster")

	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData:          UserData{CsResources: allowedres, ManagedClusters: managedClusters},
		clustersUpdatedAt: time.Now(),
		// Using current time , GetUserData should have the same values as cache
		csrUpdatedAt: time.Now(),
		nsrUpdatedAt: time.Now(),
	}
	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextAuthTokenKey, "123456")

	result, err := mock_cache.GetUserData(ctx, nil)
	if len(result.userData.CsResources) != 1 || result.userData.CsResources[0].Kind != "nodes" || result.userData.CsResources[0].Apigroup != "storage.k8s.io" ||
		result.userData.ManagedClusters[0] != "some-managed-cluster" || result.userData.ManagedClusters[1] != "some-other-managed-cluster" {
		t.Error("Cluster scoped Resources not in user cache.")
	}
	if err != nil {
		t.Error("Unexpected error while obtaining cluster scoped resources.", err)
	}

}

func Test_managedCluster_expiredCache(t *testing.T) {
	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)

	// mock clusters in user cache
	var namespaces []string
	//mock mc from shared cache
	var sharedmanagedclusters []string
	mock_cache.shared.managedClusters = append(sharedmanagedclusters, "some-managed-cluster", "some-managed-cluster1")
	mock_cache.shared.namespaces = append(namespaces, "some-managed-cluster", "some-managed-cluster1")

	//mock response objects for KUBEAPI call for SelfSubjectRulesReview
	createRule := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-managed-cluster",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{"apigroup"},
					Resources: []string{"managedclusterviews"},
				},
			},
		},
	}
	notCreateRule := &authz.SelfSubjectRulesReview{
		Spec: authz.SelfSubjectRulesReviewSpec{
			Namespace: "some-managed-cluster1",
		},
		Status: authz.SubjectRulesReviewStatus{
			ResourceRules: []authz.ResourceRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{"apigroup"},
					Resources: []string{"managedclusterviews"},
				},
			},
		},
	}

	fs := fake.NewSimpleClientset()
	fs.PrependReactor("create", "*", func(action testingk8s.Action) (handled bool, ret runtime.Object, err error) {
		ret = action.(testingk8s.CreateAction).GetObject()
		meta, ok := ret.(metav1.Object)
		if !ok {
			t.Error("Unexpected Error - expecting MetaObject with type *v1.SelfSubjectRulesReview")
			return
		}
		testSSRR := meta.(*authz.SelfSubjectRulesReview)
		//	Mimic user has Authorization to create managedclusterview
		if testSSRR.Spec.Namespace == "some-managed-cluster" {
			return true, createRule, nil
		}

		// // Mimic user has no Authorization to create managedclusterview
		return true, notCreateRule, nil

	})

	var pastManClusters []string
	pastManClusters = append(pastManClusters, "past-managed-cluster")

	last_cache_time := time.Now().Add(time.Duration(-5) * time.Minute)
	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData:          UserData{ManagedClusters: pastManClusters},
		clustersUpdatedAt: last_cache_time,
		authzClient:       fs.AuthorizationV1(),
	}

	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "123456")
	result, err := mock_cache.GetUserData(ctx, fs.AuthorizationV1())

	if len(result.userData.ManagedClusters) != 1 || result.userData.ManagedClusters[0] != "some-managed-cluster" {
		t.Errorf("Managed cluster count present in cache %d", len(result.userData.ManagedClusters))
	}
	if err != nil {
		t.Error("Unexpected error while obtaining namespaces.", err)
	}

	//  Verify that cache was updated by checking the timestamp
	if !mock_cache.users["unique-user-id"].clustersUpdatedAt.After(last_cache_time) {
		t.Error("Expected the cache.users.updatedAt to have a later timestamp")
	}

}

func Test_managedCluster_GetUserData(t *testing.T) {
	mock_cache := mockNamespaceCache()
	mock_cache = setupToken(mock_cache)

	manClusters := []string{"managed-cluster1"}
	csRes := []Resource{{Kind: "kind1", Apigroup: ""}, {Kind: "kind2", Apigroup: "v1"}}
	nsRes := make(map[string][]Resource)
	nsRes["ns1"] = []Resource{{Kind: "kind1", Apigroup: ""}, {Kind: "kind2", Apigroup: "v1"}}
	nsRes["ns2"] = []Resource{{Kind: "kind3", Apigroup: ""}, {Kind: "kind4", Apigroup: "v1"}}

	last_cache_time := time.Now().Add(time.Duration(-5) * time.Minute)
	mock_cache.users["unique-user-id"] = &UserDataCache{
		userData:          UserData{ManagedClusters: manClusters, CsResources: csRes, NsResources: nsRes},
		clustersUpdatedAt: last_cache_time,
	}
	csResResult := mock_cache.users["unique-user-id"].GetCsResources()
	if len(csResResult) != 2 {
		t.Errorf("Expected 2 clusterScoped resources but got %d", len(csResResult))

	}
	nsResResult := mock_cache.users["unique-user-id"].GetNsResources()
	if len(nsResResult) != 2 {
		t.Errorf("Expected 2 namespace Scoped resources but got %d", len(nsResResult))
	}
	mcResult := mock_cache.users["unique-user-id"].GetManagedClusters()
	if len(mcResult) != 1 {
		t.Errorf("Expected 1 managed cluster but got %d", len(mcResult))
	}
}
