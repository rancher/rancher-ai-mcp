package rbac

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	tests := map[string]struct {
		offset, limit int64
		expectedItems []int
		expectedNext  *int64
	}{
		"first page":                 {offset: 0, limit: 2, expectedItems: []int{1, 2}, expectedNext: new(int64(2))},
		"last page":                  {offset: 4, limit: 2, expectedItems: []int{5}},
		"default limit":              {offset: 0, limit: 0, expectedItems: items},
		"negative offset":            {offset: -3, limit: 2, expectedItems: []int{1, 2}, expectedNext: new(int64(2))},
		"offset past the end":        {offset: 10, limit: 2, expectedItems: []int{}},
		"offset that would overflow": {offset: math.MaxInt64, limit: 100, expectedItems: []int{}},
		"limit that would overflow":  {offset: 1, limit: math.MaxInt64, expectedItems: []int{2, 3, 4, 5}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := paginate(items, tt.offset, tt.limit)
			assert.Equal(t, tt.expectedItems, p.items)
			assert.Equal(t, tt.expectedNext, p.next)
		})
	}
}

func TestDirectGlobalRolesForUser(t *testing.T) {
	roles := directGlobalRoles{
		byUser:      map[string][]string{"u-jo": {"user"}},
		byPrincipal: map[string][]string{"openldap_user://uid=jo": {"admin", "user"}},
	}

	assert.Equal(t, []string{"admin", "user"}, roles.forUser("u-jo", []string{"local://u-jo", "openldap_user://uid=jo"}),
		"roles bound to the user's principal are included, without duplicates")
	assert.Equal(t, []string{}, roles.forUser("u-other", []string{"local://u-other"}))
	assert.Nil(t, directGlobalRoles{unavailable: true}.forUser("u-jo", nil))
}
