package tools

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"net/url"
	"strings"
)

func ParsePictureURL(pictureLink string) (uuid.UUID, error) {
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return uuid.Nil, model.ErrEvent.WithError(err).WithMessage("Failed to parse picture url").Err()
	}

	splitURL := strings.Split(parsedURL.Path, "/")

	return uuid.FromStringOrNil(splitURL[len(splitURL)-1]), nil
}

func EqualPictureURLs(pictureLink1, pictureLink2 string) bool {
	fileID1, err := ParsePictureURL(pictureLink1)
	if err != nil {
		return false
	}

	fileID2, err := ParsePictureURL(pictureLink2)
	if err != nil {
		return false
	}

	return fileID1 == fileID2
}
