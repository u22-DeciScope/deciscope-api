package mapper

import (
	"strings"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
)

func MapVerifiedIdentity(identity contract.VerifiedIdentity) (domain.IdentityInput, domain.UserSeed) {
	return domain.IdentityInput{
			Provider: identity.Provider,
			Subject:  identity.Subject,
		}, domain.UserSeed{
			Email:       strings.TrimSpace(identity.Email),
			DisplayName: strings.TrimSpace(identity.DisplayName),
			AvatarURL:   strings.TrimSpace(identity.AvatarURL),
		}
}
