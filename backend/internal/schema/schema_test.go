package schema

import (
	"errors"
	"testing"
)

func TestValidateState(t *testing.T) {
	tests := []struct {
		name          string
		trackingTable bool
		applied       int
		wantErr       error
		wantOutOfDate bool
	}{
		{
			name:          "migrated to required version is ready",
			trackingTable: true,
			applied:       RequiredVersion,
			wantErr:       nil,
		},
		{
			name:          "missing tracking table is not migrated",
			trackingTable: false,
			applied:       0,
			wantErr:       ErrNotMigrated,
		},
		{
			name:          "empty tracking table is out of date",
			trackingTable: true,
			applied:       0,
			wantErr:       nil,
			wantOutOfDate: true,
		},
		{
			name:          "partially applied migrations are out of date",
			trackingTable: true,
			applied:       RequiredVersion - 1,
			wantErr:       nil,
			wantOutOfDate: true,
		},
		{
			name:          "newer database than binary is a mismatch",
			trackingTable: true,
			applied:       RequiredVersion + 1,
			wantErr:       nil,
			wantOutOfDate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateState(tt.trackingTable, tt.applied)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("validateState() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if !tt.wantOutOfDate {
				if err != nil {
					t.Fatalf("validateState() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateState() = nil, want error")
			}
			var outOfDate *OutOfDateError
			if !errors.As(err, &outOfDate) {
				t.Fatalf("validateState() error = %v, want *OutOfDateError", err)
			}
			if outOfDate.Applied != tt.applied || outOfDate.Required != RequiredVersion {
				t.Fatalf("OutOfDateError = %+v, want applied=%d required=%d",
					outOfDate, tt.applied, RequiredVersion)
			}
		})
	}
}

func TestOutOfDateErrorMessageDistinguishesDirection(t *testing.T) {
	behind := &OutOfDateError{Applied: 1, Required: 3}
	if behind.Error() == (&OutOfDateError{Applied: 4, Required: 3}).Error() {
		t.Fatal("behind and ahead mismatches must produce distinct messages")
	}
}
