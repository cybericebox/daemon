package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

// The version list carries the first and last name of each author, never an email, from one lookup.
func TestListVersionsCarriesAuthorNames(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	exID, author := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: []byte("[]"), CreatedBy: uuid.NullUUID{UUID: author, Valid: true}},
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "published", Variants: []byte("[]")},
	}, nil)
	q.EXPECT().ListUserNames(gomock.Any(), []uuid.UUID{author}).Return([]postgres.ListUserNamesRow{
		{ID: author, FirstName: "Olena", LastName: "Koval"},
	}, nil).Times(1)

	items, err := uc.ListVersions(context.Background(), exID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].AuthorName != "Olena Koval" || items[1].AuthorName != "" {
		t.Fatalf("names = %q, %q", items[0].AuthorName, items[1].AuthorName)
	}
}

// A failed name lookup never fails the read it decorates.
func TestListVersionsSurvivesANameLookupFailure(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	exID, author := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: []byte("[]"), CreatedBy: uuid.NullUUID{UUID: author, Valid: true}},
	}, nil)
	q.EXPECT().ListUserNames(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

	items, err := uc.ListVersions(context.Background(), exID)
	if err != nil || len(items) != 1 || items[0].AuthorName != "" {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
}
