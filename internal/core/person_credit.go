package core

// PersonCredit is one person's credit on one title, as the catalogue
// stores it: the kind ("cast", "director", "writer", "crew") and the
// person as credited there (name, role, portrait, ids).
type PersonCredit struct {
	Key    RatingKey
	Kind   string
	Person Person
}
