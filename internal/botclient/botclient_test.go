package botclient

import (
	"errors"
	"testing"
)

func TestResolveDatabaseURL(t *testing.T) {
	const ro = "postgres://botclient_ro:pw@localhost:55432/simpleai"
	const rw = "postgres://simpleai:pw@localhost:55432/simpleai"

	tests := []struct {
		name    string
		opt     Options
		want    string
		wantErr error
	}{
		{
			name: "default read-only returns RO url",
			opt:  Options{AllowWrites: false, ReadOnlyURL: ro, WriteURL: rw},
			want: ro,
		},
		{
			name:    "read-only without RO url errors",
			opt:     Options{AllowWrites: false, ReadOnlyURL: "", WriteURL: rw},
			wantErr: ErrNoReadOnlyURL,
		},
		{
			name: "allow-writes returns RW url",
			opt:  Options{AllowWrites: true, ReadOnlyURL: ro, WriteURL: rw},
			want: rw,
		},
		{
			name:    "allow-writes without RW url errors (not silent RO fallback)",
			opt:     Options{AllowWrites: true, ReadOnlyURL: ro, WriteURL: ""},
			wantErr: ErrNoWriteURL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDatabaseURL(tt.opt)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want err %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}
