package server

import (
	"net/http"
	"testing"
)

// TestModerationOverHTTP walks the role ladder through the API: the Abbot
// appoints a Bishop and a Warden, the Bishop appoints a Warden, and the
// Warden mutes and temporarily bans a member.
func TestModerationOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	abbot, _ := signUp(t, ts, "abbot", "")
	var d domainJSON
	call(t, ts, "POST", "/api/v1/domains", abbot.token, map[string]string{"name": "Ladder"}, &d)
	var sm struct{ Summons string }
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/summons", abbot.token, map[string]int{"max_uses": 0}, &sm)
	bishop, _ := signUp(t, ts, "bishop", sm.Summons)
	warden, _ := signUp(t, ts, "warden", sm.Summons)
	member, _ := signUp(t, ts, "member", sm.Summons)
	other, _ := signUp(t, ts, "other", sm.Summons)

	var detail struct {
		Roles []struct{ ID, Name string } `json:"roles"`
	}
	call(t, ts, "GET", "/api/v1/domains/"+d.ID, abbot.token, nil, &detail)
	role := map[string]string{}
	for _, r := range detail.Roles {
		role[r.Name] = r.ID
	}
	base := "/api/v1/domains/" + d.ID
	setRole := func(actor *client, target *client, name string) int {
		return call(t, ts, "POST", base+"/members/"+target.userID+"/role", actor.token, map[string]string{"role_id": role[name]}, nil)
	}
	expect := func(what string, got, want int) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: got %d, want %d", what, got, want)
		}
	}

	expect("abbot appoints bishop", setRole(abbot, bishop, "Bishop"), http.StatusNoContent)
	expect("abbot appoints warden", setRole(abbot, warden, "Warden"), http.StatusNoContent)
	expect("bishop appoints warden", setRole(bishop, other, "Warden"), http.StatusNoContent)
	expect("bishop cannot appoint bishop", setRole(bishop, member, "Bishop"), http.StatusForbidden)
	expect("bishop demotes warden", setRole(bishop, other, "Brother / Sister"), http.StatusNoContent)
	expect("warden cannot appoint warden", setRole(warden, member, "Warden"), http.StatusForbidden)
	expect("warden cannot act on bishop", setRole(warden, bishop, "Postulant"), http.StatusForbidden)

	expect("warden mutes member", call(t, ts, "POST", base+"/members/"+member.userID+"/mute", warden.token, nil, nil), http.StatusNoContent)
	expect("warden cannot mute bishop", call(t, ts, "POST", base+"/members/"+bishop.userID+"/mute", warden.token, nil, nil), http.StatusForbidden)
	expect("warden cannot kick bishop", call(t, ts, "POST", base+"/members/"+bishop.userID+"/kick", warden.token, nil, nil), http.StatusForbidden)
	expect("warden cannot ban bishop", call(t, ts, "POST", base+"/members/"+bishop.userID+"/ban", warden.token,
		map[string]any{"duration_seconds": 3600}, nil), http.StatusForbidden)
	expect("bad duration", call(t, ts, "POST", base+"/members/"+member.userID+"/ban", warden.token,
		map[string]any{"duration_seconds": -1}, nil), http.StatusBadRequest)
	expect("warden bans member for a day", call(t, ts, "POST", base+"/members/"+member.userID+"/ban", warden.token,
		map[string]any{"duration_seconds": 86400, "reason": "spam"}, nil), http.StatusNoContent)
	expect("banned member reads domain", call(t, ts, "GET", base, member.token, nil, nil), http.StatusForbidden)
	expect("banned member accepts summons", call(t, ts, "POST", "/api/v1/summons/"+sm.Summons+"/accept", member.token, nil, nil), http.StatusForbidden)

	var bans []struct {
		UserID     string `json:"user_id"`
		Callsign   string `json:"callsign"`
		BannedBy   string `json:"banned_by"`
		ExpiresAt  int64  `json:"expires_at"`
		FormerRank int    `json:"former_rank"`
	}
	expect("warden lists bans", call(t, ts, "GET", base+"/bans", warden.token, nil, &bans), http.StatusOK)
	if len(bans) != 1 || bans[0].Callsign != "member" || bans[0].BannedBy != "warden" || bans[0].ExpiresAt == 0 || bans[0].FormerRank != 100 {
		t.Fatalf("bans = %+v", bans)
	}
	expect("member lists bans", call(t, ts, "GET", base+"/bans", other.token, nil, nil), http.StatusForbidden)
	expect("member unbans", call(t, ts, "DELETE", base+"/bans/"+member.userID, other.token, nil, nil), http.StatusForbidden)
	expect("warden unbans", call(t, ts, "DELETE", base+"/bans/"+member.userID, warden.token, nil, nil), http.StatusNoContent)
	expect("member rejoins", call(t, ts, "POST", "/api/v1/summons/"+sm.Summons+"/accept", member.token, nil, nil), http.StatusOK)
}
