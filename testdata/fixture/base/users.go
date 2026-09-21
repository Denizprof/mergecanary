package fixture

import "fmt"

func getUser(id int) string {
	return fmt.Sprintf("user-%d", id)
}
