package wizard

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

// fakeObjectStore stands in for MinIO. There is no object store in the test
// environment, and the defect is exactly which calls setup does and does not
// make against one, so the calls are what these tests observe.
type fakeObjectStore struct {
	existing map[string]bool

	made    []string
	put     []string
	removed []string

	existsErr error
	makeErr   error
}

func newFakeObjectStore(existing ...string) *fakeObjectStore {
	f := &fakeObjectStore{existing: map[string]bool{}}
	for _, bucket := range existing {
		f.existing[bucket] = true
	}
	return f
}

// factory hands the same fake to every call, so a test can look at what setup
// did to it once setup has finished.
func (f *fakeObjectStore) factory() objectStoreFactory {
	return func(Data) (objectStore, error) { return f, nil }
}

func (f *fakeObjectStore) BucketExists(_ context.Context, bucket string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.existing[bucket], nil
}

func (f *fakeObjectStore) MakeBucket(_ context.Context, bucket string, _ minio.MakeBucketOptions) error {
	f.made = append(f.made, bucket)
	if f.makeErr != nil {
		return f.makeErr
	}
	f.existing[bucket] = true
	return nil
}

func (f *fakeObjectStore) PutObject(_ context.Context, bucket, object string, _ io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	f.put = append(f.put, object)
	return minio.UploadInfo{Bucket: bucket, Key: object}, nil
}

func (f *fakeObjectStore) RemoveObject(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
	f.removed = append(f.removed, object)
	return nil
}

func (f *fakeObjectStore) hasBucket(bucket string) bool { return f.existing[bucket] }

func TestEnsureObjectStorageCreatesTheBucketWhenItIsMissing(t *testing.T) {
	store := newFakeObjectStore()
	svc := &Service{newStore: store.factory()}

	data := completeData()
	if err := svc.EnsureObjectStorage(context.Background(), data); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if len(store.made) != 1 || store.made[0] != data.MinIOBucket {
		t.Fatalf("expected the bucket %q to be created, got %v", data.MinIOBucket, store.made)
	}
}

// Re-running setup, or booting a second replica, must not turn "the bucket is
// already there" into a failure.
func TestEnsureObjectStorageIsANoOpWhenTheBucketExists(t *testing.T) {
	data := completeData()
	store := newFakeObjectStore(data.MinIOBucket)
	svc := &Service{newStore: store.factory()}

	if err := svc.EnsureObjectStorage(context.Background(), data); err != nil {
		t.Fatalf("ensure on an existing bucket must succeed, got: %v", err)
	}
	if len(store.made) != 0 {
		t.Errorf("an existing bucket must not be re-created, got %v", store.made)
	}
}

// Replicas boot together: each checks, finds nothing, and creates. The loser of
// that race is told the bucket already exists, which is the outcome it wanted.
func TestEnsureObjectStorageToleratesLosingTheCreateRace(t *testing.T) {
	data := completeData()
	store := newFakeObjectStore()
	store.makeErr = minio.ErrorResponse{Code: "BucketAlreadyOwnedByYou", Message: "Your previous request to create the named bucket succeeded"}
	svc := &Service{newStore: store.factory()}

	if err := svc.EnsureObjectStorage(context.Background(), data); err != nil {
		t.Fatalf("losing the create race must not fail setup, got: %v", err)
	}
}

// The failure that started all this was silent. Whatever else happens, the
// operator has to be told which bucket could not be created and why.
func TestEnsureObjectStorageReportsWhatItCouldNotCreate(t *testing.T) {
	data := completeData()
	cause := errors.New("dial tcp 127.0.0.1:9000: connection refused")

	tests := []struct {
		name  string
		store *fakeObjectStore
	}{
		{"create fails", func() *fakeObjectStore {
			s := newFakeObjectStore()
			s.makeErr = cause
			return s
		}()},
		{"the check itself fails", func() *fakeObjectStore {
			s := newFakeObjectStore()
			s.existsErr = cause
			return s
		}()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{newStore: tc.store.factory()}

			err := svc.EnsureObjectStorage(context.Background(), data)
			if err == nil {
				t.Fatal("an unreachable object store must be reported, not swallowed")
			}
			if !strings.Contains(err.Error(), data.MinIOBucket) {
				t.Errorf("error should name the bucket %q, got: %v", data.MinIOBucket, err)
			}
			if !errors.Is(err, cause) {
				t.Errorf("error should carry the underlying cause, got: %v", err)
			}
		})
	}
}

// The step-4 button both provisions the bucket and proves an upload can
// happen. Bucket creation now lives in EnsureObjectStorage; the write test
// must not have gone with it.
func TestTestServiceMinIOCreatesTheBucketAndWritesToIt(t *testing.T) {
	store := newFakeObjectStore()
	svc := &Service{newStore: store.factory()}
	data := completeData()

	if err := svc.TestService(context.Background(), "minio", data); err != nil {
		t.Fatalf("test connection: %v", err)
	}

	if !store.hasBucket(data.MinIOBucket) {
		t.Errorf("the connection test must leave the bucket %q in place", data.MinIOBucket)
	}
	if len(store.put) != 1 {
		t.Errorf("the connection test must write an object, wrote %v", store.put)
	}
	if len(store.removed) != len(store.put) {
		t.Errorf("the test object must be cleaned up: wrote %v, removed %v", store.put, store.removed)
	}
}

func TestEnsureObjectStorageRequiresAnEndpointAndBucket(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Data)
	}{
		{"no endpoint", func(d *Data) { d.MinIOEndpoint = "" }},
		{"no bucket", func(d *Data) { d.MinIOBucket = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := completeData()
			tc.mutate(&data)
			store := newFakeObjectStore()
			svc := &Service{newStore: store.factory()}

			if err := svc.EnsureObjectStorage(context.Background(), data); err == nil {
				t.Fatal("an unconfigured object store must be reported")
			}
			if len(store.made) != 0 {
				t.Errorf("nothing should have been created, got %v", store.made)
			}
		})
	}
}

// An outside bucket is given with its scheme, as https://s3.<region>.amazonaws.com,
// and the S3 client refuses an address with one. Setup's own client has to take
// it apart, or the bucket check fails before the organization is created.
func TestSetupReachesStorageGivenWithOrWithoutAScheme(t *testing.T) {
	for _, tc := range []struct {
		endpoint, host string
		secure         bool
	}{
		{"minio:9000", "minio:9000", false},
		{"http://minio:9000", "minio:9000", false},
		{"https://s3.ap-south-1.amazonaws.com", "s3.ap-south-1.amazonaws.com", true},
		{"https://s3.ap-south-1.amazonaws.com/", "s3.ap-south-1.amazonaws.com", true},
		{"httpsfiles.example.com", "httpsfiles.example.com", false},
	} {
		d := Data{MinIOEndpoint: tc.endpoint, MinIOAccessKey: "k", MinIOSecretKey: "s", MinIOBucket: "b"}
		if got := d.MinIOHost(); got != tc.host {
			t.Errorf("%s: host %q, want %q", tc.endpoint, got, tc.host)
		}
		if got := d.MinIOSecure(); got != tc.secure {
			t.Errorf("%s: secure %v, want %v", tc.endpoint, got, tc.secure)
		}
		if _, err := minioClientFor(d); err != nil {
			t.Errorf("%s: the client refused it: %v", tc.endpoint, err)
		}
	}
}
