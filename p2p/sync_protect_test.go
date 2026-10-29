package p2p

import "testing"

func TestSyncPeersAreProtectedOnlyDuringARun(t *testing.T) {
	a := mustNewTestNode(t)
	defer a.Stop()
	b := mustNewTestNode(t)
	defer b.Stop()
	mustConnectNodes(t, a, b)

	sm := &SyncManager{node: a}
	connMgr := a.host.ConnManager()
	release := sm.protectSyncPeers([]PeerStatus{{Peer: b.PeerID()}})
	if !connMgr.IsProtected(b.PeerID(), syncProtectTag) {
		t.Fatal("sync peer is not protected during the run")
	}
	release()
	if connMgr.IsProtected(b.PeerID(), syncProtectTag) {
		t.Fatal("sync peer is still protected after the run")
	}
}
