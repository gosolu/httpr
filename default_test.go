package httpr

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultClient_LazyInit(t *testing.T) {
	// Reset to nil first
	SetDefaultClient(nil)

	c1 := DefaultClient()
	if c1 == nil {
		t.Fatal("expected non-nil default client")
	}

	c2 := DefaultClient()
	if c1 != c2 {
		t.Fatal("expected DefaultClient to return the same instance")
	}
}

func TestSetDefaultClient(t *testing.T) {
	customClient := NewClient(
		WithMaxRetries(5),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)
	SetDefaultClient(customClient)

	if DefaultClient() != customClient {
		t.Fatal("expected DefaultClient to return the customized client")
	}

	// Reset back to nil and verify reinitialization
	SetDefaultClient(nil)
	reinit := DefaultClient()
	if reinit == nil || reinit == customClient {
		t.Fatal("expected DefaultClient to create a new default client after reset")
	}
}

func TestPackageLevelMethods(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        string
		attempts       int32
	)

	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		gotMethod = req.Method
		gotContentType = req.Header.Get("Content-Type")
		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			gotBody = string(b)
		} else {
			gotBody = ""
		}
		return stringResponse(http.StatusOK, "success"), nil
	})

	testClient := NewClient(
		WithMaxRetries(2),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)
	SetDefaultClient(testClient)
	defer SetDefaultClient(nil)

	ctx := context.Background()

	// 1. Get
	resp, err := Get(ctx, "http://example.com/get")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Get failed: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	resp.Body.Close()

	// 2. Head
	resp, err = Head(ctx, "http://example.com/head")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Head failed: %v", err)
	}
	if gotMethod != http.MethodHead {
		t.Errorf("expected HEAD, got %s", gotMethod)
	}
	resp.Body.Close()

	// 3. Post
	resp, err = Post(ctx, "http://example.com/post", "application/json", strings.NewReader(`{"key":"val"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Post failed: %v", err)
	}
	if gotMethod != http.MethodPost || gotContentType != "application/json" || gotBody != `{"key":"val"}` {
		t.Errorf("Post mismatch: method=%s, type=%s, body=%s", gotMethod, gotContentType, gotBody)
	}
	resp.Body.Close()

	// 4. Put
	resp, err = Put(ctx, "http://example.com/put", "text/plain", strings.NewReader("updated data"))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Put failed: %v", err)
	}
	if gotMethod != http.MethodPut || gotContentType != "text/plain" || gotBody != "updated data" {
		t.Errorf("Put mismatch: method=%s, type=%s, body=%s", gotMethod, gotContentType, gotBody)
	}
	resp.Body.Close()

	// 5. Delete
	resp, err = Delete(ctx, "http://example.com/delete")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Delete failed: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	resp.Body.Close()

	// 6. PostForm
	form := url.Values{"username": {"alice"}, "role": {"admin"}}
	resp, err = PostForm(ctx, "http://example.com/form", form)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("PostForm failed: %v", err)
	}
	if gotMethod != http.MethodPost || gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("PostForm mismatch: method=%s, type=%s", gotMethod, gotContentType)
	}
	resp.Body.Close()

	// 7. Do
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, "http://example.com/patch", strings.NewReader("patch data"))
	resp, err = Do(ctx, req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Do failed: %v", err)
	}
	if gotMethod != http.MethodPatch || gotBody != "patch data" {
		t.Errorf("Do mismatch: method=%s, body=%s", gotMethod, gotBody)
	}
	resp.Body.Close()
}

func TestClient_PutAndDeleteDirect(t *testing.T) {
	var gotMethod, gotContentType, gotBody string
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotMethod = req.Method
		gotContentType = req.Header.Get("Content-Type")
		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			gotBody = string(b)
		} else {
			gotBody = ""
		}
		return stringResponse(http.StatusOK, "ok"), nil
	})

	client := NewClient(WithTransport(mock))
	ctx := context.Background()

	// Test Client.Put
	resp, err := client.Put(ctx, "http://example.com/put", "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	resp.Body.Close()
	if gotMethod != http.MethodPut || gotContentType != "application/json" || gotBody != `{"a":1}` {
		t.Errorf("Put mismatch: %s, %s, %s", gotMethod, gotContentType, gotBody)
	}

	// Test Client.Delete
	resp, err = client.Delete(ctx, "http://example.com/del")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	resp.Body.Close()
	if gotMethod != http.MethodDelete {
		t.Errorf("Delete mismatch: %s", gotMethod)
	}
}

func TestPackageLevelMethods_InvalidURLs(t *testing.T) {
	ctx := context.Background()
	badURL := "://invalid-url"

	if _, err := Get(ctx, badURL); err == nil {
		t.Error("expected error on invalid URL Get")
	}
	if _, err := Head(ctx, badURL); err == nil {
		t.Error("expected error on invalid URL Head")
	}
	if _, err := Post(ctx, badURL, "text/plain", nil); err == nil {
		t.Error("expected error on invalid URL Post")
	}
	if _, err := Put(ctx, badURL, "text/plain", nil); err == nil {
		t.Error("expected error on invalid URL Put")
	}
	if _, err := Delete(ctx, badURL); err == nil {
		t.Error("expected error on invalid URL Delete")
	}
	if _, err := clientBadURL(t).Put(ctx, badURL, "", nil); err == nil {
		t.Error("expected error on Client.Put with bad URL")
	}
	if _, err := clientBadURL(t).Delete(ctx, badURL); err == nil {
		t.Error("expected error on Client.Delete with bad URL")
	}
}

func clientBadURL(t *testing.T) *Client {
	return NewClient()
}

func TestDefaultClient_ConcurrentAccess(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, "ok"), nil
	})
	SetDefaultClient(NewClient(WithTransport(mock)))
	defer SetDefaultClient(nil)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			switch idx % 5 {
			case 0:
				resp, err := Get(ctx, "http://example.com")
				if err == nil {
					resp.Body.Close()
				}
			case 1:
				resp, err := Post(ctx, "http://example.com", "text/plain", strings.NewReader("test"))
				if err == nil {
					resp.Body.Close()
				}
			case 2:
				resp, err := Put(ctx, "http://example.com", "text/plain", strings.NewReader("test"))
				if err == nil {
					resp.Body.Close()
				}
			case 3:
				resp, err := Delete(ctx, "http://example.com")
				if err == nil {
					resp.Body.Close()
				}
			case 4:
				_ = DefaultClient()
			}
		}(i)
	}

	wg.Wait()
}
