package fixture

import "fmt"

func fetchUser(id int) string {
	return fmt.Sprintf("user-%d", id)
}
