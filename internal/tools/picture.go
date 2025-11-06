package tools

import (
	"net/url"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

func ParsePictureURL(pictureLink string) (uuid.UUID, error) {
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to parse picture url").Err()
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
