package security

import "testing"

func TestAbandonOperatorOnly(t *testing.T){
	path:="/api/v1/deliveries/example/abandon"
	if !roleAllowed("operator","POST",path)||roleAllowed("collaborator","POST",path)||roleAllowed("viewer","POST",path){t.Fatal("abandon must be operator-only")}
}
