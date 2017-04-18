package actions_test

import (
	"errors"
	"time"

	"code.cloudfoundry.org/clock/fakeclock"

	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/runtime"
	"k8s.io/client-go/1.4/pkg/watch"
	"k8s.io/client-go/1.4/testing"

	"github.com/ScarletTanager/kubernetes-cpi/actions"
	"github.com/ScarletTanager/kubernetes-cpi/cpi"
	"github.com/ScarletTanager/kubernetes-cpi/kubecluster/fakes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateDisk", func() {
	var (
		fakeClient   *fakes.Client
		fakeProvider *fakes.ClientProvider
		fakeWatch    *watch.FakeWatcher
		cloudProps   actions.CreateDiskCloudProperties

		diskCreator    *actions.DiskCreator
		pvcMeta        v1.ObjectMeta
		initialPvcSpec v1.PersistentVolumeClaimSpec

		// Just needed to make things work, not actually used...
		vmcid cpi.VMCID
	)

	BeforeEach(func() {
		pvcMeta = v1.ObjectMeta{
			Name:      "disk-disk-guid",
			Namespace: "bosh-namespace",
			Annotations: map[string]string{
				"annotation-key": "annotation-value",
			},
			Labels: map[string]string{
				"key": "value",
			},
		}

		res, _ := resource.ParseQuantity("1000Mi")

		initialPvcSpec = v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceStorage: res,
				},
			},
		}

		fakeClient = fakes.NewClient()
		fakeClient.ContextReturns("bosh")
		fakeClient.NamespaceReturns("bosh-namespace")

		fakeWatch = watch.NewFakeWithChanSize(1)
		fakeWatch.Modify(&v1.PersistentVolumeClaim{
			ObjectMeta: pvcMeta,
			Spec:       initialPvcSpec,
			Status: v1.PersistentVolumeClaimStatus{
				Phase:       v1.ClaimBound,
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
				Capacity: v1.ResourceList{
					v1.ResourceStorage: res,
				},
			},
		})

		fakeClient.PrependWatchReactor("*", testing.DefaultWatchReactor(fakeWatch, nil))

		fakeProvider = &fakes.ClientProvider{}
		fakeProvider.NewReturns(fakeClient, nil)

		cloudProps = actions.CreateDiskCloudProperties{
			Context: "bosh",
		}

		diskCreator = &actions.DiskCreator{
			ClientProvider:    fakeProvider,
			Clock:             fakeclock.NewFakeClock(time.Now()),
			DiskReadyTimeout:  5 * time.Second,
			GUIDGeneratorFunc: func() (string, error) { return "disk-guid", nil },
		}

		vmcid = "vm-guid"
	})

	It("gets a client for the appropriate context", func() {
		_, err := diskCreator.CreateDisk(1000, cloudProps, vmcid)
		Expect(err).NotTo(HaveOccurred())

		Expect(fakeProvider.NewCallCount()).To(Equal(1))
		Expect(fakeProvider.NewArgsForCall(0)).To(Equal("bosh"))
	})

	// Skip for now until we decide where to go with volumes
	XIt("creates a persistent volume", func() {
		diskCID, err := diskCreator.CreateDisk(1000, cloudProps, vmcid)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskCID).To(Equal(cpi.DiskCID("bosh:disk-guid")))

		matches := fakeClient.MatchingActions("create", "persistentvolumes")
		Expect(matches).To(HaveLen(1))

		createAction := matches[0].(testing.CreateAction)

		pv := createAction.GetObject().(*v1.PersistentVolume)
		Expect(pv).To(Equal(&v1.PersistentVolume{
			ObjectMeta: v1.ObjectMeta{
				Name: "volume-disk-guid",
				Labels: map[string]string{
					"bosh.cloudfoundry.org/disk-id": "disk-guid",
				},
			},
			Spec: v1.PersistentVolumeSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
				Capacity: v1.ResourceList{
					v1.ResourceStorage: resource.MustParse("1000Mi"),
				},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimRecycle,
			},
		}))
	})

	It("creates a persistent volume claim", func() {
		diskCID, err := diskCreator.CreateDisk(1000, cloudProps, vmcid)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskCID).To(Equal(cpi.DiskCID("bosh:disk-guid")))

		matches := fakeClient.MatchingActions("create", "persistentvolumeclaims")
		Expect(matches).To(HaveLen(1))

		createAction := matches[0].(testing.CreateAction)
		Expect(createAction.GetNamespace()).To(Equal("bosh-namespace"))

		pvc := createAction.GetObject().(*v1.PersistentVolumeClaim)
		Expect(pvc).To(Equal(&v1.PersistentVolumeClaim{
			ObjectMeta: v1.ObjectMeta{
				Name:      "disk-disk-guid",
				Namespace: "bosh-namespace",
				Labels: map[string]string{
					"bosh.cloudfoundry.org/disk-id": "disk-guid",
				},
				Annotations: map[string]string{
					"volume.beta.kubernetes.io/storage-class":       "ibmc-file-bronze",
					"volume.beta.kubernetes.io/storage-provisioner": "ibm.io/ibmc-file",
				},
			},
			Spec: v1.PersistentVolumeClaimSpec{
				// VolumeName:  "volume-disk-guid",
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("1000Mi"),
					},
				},
			},
		}))
	})

	Context("when getting the client fails", func() {
		BeforeEach(func() {
			fakeProvider.NewReturns(nil, errors.New("boom"))
		})

		It("gets a client for the appropriate context", func() {
			_, err := diskCreator.CreateDisk(1000, cloudProps, vmcid)
			Expect(err).To(MatchError("boom"))
		})
	})

	Context("when creating the persistent volume claim fails", func() {
		BeforeEach(func() {
			fakeClient.PrependReactor("create", "persistentvolumeclaims", func(action testing.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("create-pvc-welp")
			})
		})

		It("returns an error", func() {
			_, err := diskCreator.CreateDisk(1000, cloudProps, vmcid)
			Expect(err).To(MatchError("create-pvc-welp"))
			Expect(fakeClient.MatchingActions("create", "persistentvolumeclaims")).To(HaveLen(1))
		})
	})
})
