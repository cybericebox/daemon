package tools

import (
	"crypto/rand"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"math/big"
)

const (
	flagSymbols = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&"
)

func GetSolutionForTask(solutions ...string) (string, error) {
	if len(solutions) == 0 {
		return getRandSolution()
	}
	if len(solutions) == 1 {
		return solutions[0], nil
	}

	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(solutions))))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to choose solution from list").Cause()
	}
	return solutions[i.Int64()], nil
}

func getRandSolution() (string, error) {
	str := ""

	for i := 0; i < config.RandomFlagLength; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(flagSymbols))))
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate random solution").Cause()
		}
		str += string(flagSymbols[n.Int64()])
	}

	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(str)-12)))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate random solution").Cause()
	}

	i = big.NewInt(i.Int64() + 4)

	j, err := rand.Int(rand.Reader, big.NewInt(int64(len(str))-i.Int64()-4))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate random solution").Cause()
	}
	j = big.NewInt(i.Int64() + j.Int64() + 4)

	return fmt.Sprintf(config.FlagFormat, str[:i.Int64()]+"-"+str[i.Int64():j.Int64()]+"-"+str[j.Int64():]), nil
}
