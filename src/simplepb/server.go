package simplepb

//
// This is a outline of primary-backup replication based on a simplifed version of Viewstamp replication.
//
//
//

import (
	"sync"
	// "fmt"
	"labrpc"
)

// the 3 possible server status
const (
	NORMAL = iota
	VIEWCHANGE
	RECOVERING
)

// PBServer defines the state of a replica server (either primary or backup)
type PBServer struct {
	mu             sync.Mutex          // Lock to protect shared access to this peer's state
	peers          []*labrpc.ClientEnd // RPC end points of all peers
	me             int                 // this peer's index into peers[]
	currentView    int                 // what this peer believes to be the current active view
	status         int                 // the server's current status (NORMAL, VIEWCHANGE or RECOVERING)
	lastNormalView int                 // the latest view which had a NORMAL status

	log         []interface{} // the log of "commands"
	commitIndex int           // all log entries <= commitIndex are considered to have been committed.

	// ... other state that you might need ...
	prepareRetry int		// number of retries for prepare
}

// Prepare defines the arguments for the Prepare RPC
// Note that all field names must start with a capital letter for an RPC args struct
type PrepareArgs struct {
	View          int         // the primary's current view
	PrimaryCommit int         // the primary's commitIndex
	Index         int         // the index position at which the log entry is to be replicated on backups
	Entry         interface{} // the log entry to be replicated
}

// PrepareReply defines the reply for the Prepare RPC
// Note that all field names must start with a capital letter for an RPC reply struct
type PrepareReply struct {
	View    int  // the backup's current view
	Success bool // whether the Prepare request has been accepted or rejected
}

// RecoverArgs defined the arguments for the Recovery RPC
type RecoveryArgs struct {
	View   int // the view that the backup would like to synchronize with
	Server int // the server sending the Recovery RPC (for debugging)
}

type RecoveryReply struct {
	View          int           // the view of the primary
	Entries       []interface{} // the primary's log including entries replicated up to and including the view.
	PrimaryCommit int           // the primary's commitIndex
	Success       bool          // whether the Recovery request has been accepted or rejected
}

type ViewChangeArgs struct {
	View int // the new view to be changed into
}

type ViewChangeReply struct {
	LastNormalView int           // the latest view which had a NORMAL status at the server
	Log            []interface{} // the log at the server
	Success        bool          // whether the ViewChange request has been accepted/rejected
}

type StartViewArgs struct {
	View int           // the new view which has completed view-change
	Log  []interface{} // the log associated with the new new
}

type StartViewReply struct {
}

// GetPrimary is an auxilary function that returns the server index of the
// primary server given the view number (and the total number of replica servers)
func GetPrimary(view int, nservers int) int {
	return view % nservers
}

// IsCommitted is called by tester to check whether an index position
// has been considered committed by this server
func (srv *PBServer) IsCommitted(index int) (committed bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.commitIndex >= index {
		return true
	}
	return false
}

// ViewStatus is called by tester to find out the current view of this server
// and whether this view has a status of NORMAL.
func (srv *PBServer) ViewStatus() (currentView int, statusIsNormal bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.currentView, srv.status == NORMAL
}

// GetEntryAtIndex is called by tester to return the command replicated at
// a specific log index. If the server's log is shorter than "index", then
// ok = false, otherwise, ok = true
func (srv *PBServer) GetEntryAtIndex(index int) (ok bool, command interface{}) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.log) > index {
		return true, srv.log[index]
	}
	return false, command
}

// Kill is called by tester to clean up (e.g. stop the current server)
// before moving on to the next test
func (srv *PBServer) Kill() {
	// Your code here, if necessary
	srv = nil
}

// Make is called by tester to create and initalize a PBServer
// peers is the list of RPC endpoints to every server (including self)
// me is this server's index into peers.
// startingView is the initial view (set to be zero) that all servers start in
func Make(peers []*labrpc.ClientEnd, me int, startingView int) *PBServer {
	srv := &PBServer{
		peers:          peers,
		me:             me,
		currentView:    startingView,
		lastNormalView: startingView,
		status:         NORMAL,
	}
	// all servers' log are initialized with a dummy command at index 0
	var v interface{}
	srv.log = append(srv.log, v)

	// Your other initialization code here, if there's any
	return srv
}


// Start() is invoked by tester on some replica server to replicate a
// command.  Only the primary should process this request by appending
// the command to its log and then return *immediately* (while the log is being replicated to backup servers).
// if this server isn't the primary, returns false.
// Note that since the function returns immediately, there is no guarantee that this command
// will ever be committed upon return, since the primary
// may subsequently fail before replicating the command to all servers
//
// The first return value is the index that the command will appear at
// *if it's eventually committed*. The second return value is the current
// view. The third return value is true if this server believes it is
// the primary.
func (srv *PBServer) Start(command interface{}) (
	index int, view int, ok bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	// fmt.Printf("Start server %d\n", srv.me)
	// do not process command if status is not NORMAL
	// and if i am not the primary in the current view
	if srv.status != NORMAL {
		return -1, srv.currentView, false
	} else if GetPrimary(srv.currentView, len(srv.peers)) != srv.me {
		return -1, srv.currentView, false
	}
	// Your code here
	srv.lastNormalView = srv.currentView
	// Append command to the log
	srv.log = append(srv.log, command)
	index = len(srv.log)-1
	view = srv.currentView
	ok = true
	// Send prepare messages in a new thread and return immediately
	go srv.issuePrepares(view, command, index, srv.commitIndex)
	return index, view, ok
}



// exmple code to send an AppendEntries RPC to a server.
// server is the index of the target server in srv.peers[].
// expects RPC arguments in args.
// The RPC library fills in *reply with RPC reply, so caller should pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
func (srv *PBServer) sendPrepare(server int, args *PrepareArgs, reply *PrepareReply) bool {
	ok := srv.peers[server].Call("PBServer.Prepare", args, reply)
	return ok
}
func(srv *PBServer) issuePrepares(view int, command interface{}, index int, commitIndex int){
	workChan := make(chan *PrepareReply, len(srv.peers))
	for n := 0; n < len(srv.peers); n++ {
		if n == srv.me {
			continue
		}
		go func(i int) {
			// fmt.Printf("Send prepare from %d to %d with Index %d\n", srv.me, i, index)
			args := PrepareArgs{View: view, PrimaryCommit: commitIndex, Index: index, Entry: command}
			var reply PrepareReply
			res := srv.sendPrepare(i, &args, &reply)
			// fmt.Printf("Prepare response from server i=%d, res=%v, reply=%v\n",i,res,reply.Success)
			if (res == true) {
				workChan <- &reply
			} else {
				workChan <- nil
			}
		}(n)
	}
	// wait to receive prepare requests
	go func(){
		var successReplies []*PrepareReply
		var nReplies int
		majority := len(srv.peers)/2
		for r := range workChan {
			nReplies++
			if r != nil && r.Success {
				successReplies = append(successReplies, r)
			}
			if nReplies == len(srv.peers)-1 || len(successReplies) == majority {
				break
			}
		}
		if len(successReplies) >= majority {
			// wait previous requests to be committed
			for {
				srv.mu.Lock()
				if srv.commitIndex+1 == index {
					// fmt.Printf("Commited %d\n", index)
					srv.commitIndex = index
					srv.mu.Unlock()
					break
				}
				srv.mu.Unlock()
			}
		} else {
			// resend the request
			// fmt.Printf("Updated View %d\n", updatedView)
			// fmt.Printf("Re-send view %d, command %d, index %d, commirIndex %d\n", view, command, index, commitIndex)
			go srv.issuePrepares(view, command, index, commitIndex)
		}
	}()
}
// Send RPC for recovery
func (srv *PBServer) sendRecovery(server int, args *RecoveryArgs, reply *RecoveryReply) bool {
	ok := srv.peers[server].Call("PBServer.Recovery", args, reply)
	return ok
}

// Prepare is the RPC handler for the Prepare RPC
func (srv *PBServer) Prepare(args *PrepareArgs, reply *PrepareReply) {
	// Your code here
	srv.mu.Lock()
	// fmt.Printf("Prepare Handling for server %d, request view %d, index %d, commit %d\n", srv.me, args.View, args.Index, args.PrimaryCommit)
	if (srv.currentView == args.View && len(srv.log) == args.Index) {
		srv.prepareRetry = 0
		// fmt.Printf("Success server %d: Log index %d\n", srv.me, len(srv.log))
		srv.lastNormalView = srv.currentView
		srv.log = append(srv.log, args.Entry)
		reply.Success, reply.View  = true, args.View
		// Piggyback Commit
		srv.commitIndex = args.PrimaryCommit
		srv.mu.Unlock()
		return
	}
	if (srv.currentView == args.View && len(srv.log) > args.Index) {
		// fmt.Printf("Server %d length of log is greater. %v\n", srv.me, srv.log)
		reply.Success, reply.View = true, args.View
		srv.mu.Unlock()
		return
	}
	if (srv.currentView > args.View) {
		// fmt.Printf("Server %d current view %d is greater than requested view %d\n", srv.me, srv.currentView, args.View)
		reply.Success = false
		srv.mu.Unlock()
		return
	}
	// If the current index or view is outdated, and the number of retries exceeds the threshold, then do recovery
	if srv.prepareRetry >= 100 {
		// fmt.Printf("Retry Initiated for server %d\n", srv.me, srv.currentView, args.View)
		srv.prepareRetry = 0
		srv.status = RECOVERING
		// Recovery
		go func(view int) {
			// fmt.Printf("Send recovery from %d to %d \n", srv.me, GetPrimary(view, len(srv.peers)))
			rcArgs := RecoveryArgs{View: view, Server: srv.me}
			var rcReply RecoveryReply
			res := srv.sendRecovery(GetPrimary(view, len(srv.peers)), &rcArgs, &rcReply)
			if (res == true && rcReply.Success == true) {
				// Change the server state
				srv.mu.Lock()
				// fmt.Printf("Recovery of server %d to view %d from %d with commit %d, log %v\n", srv.me, view, GetPrimary(view, len(srv.peers)), rcReply.PrimaryCommit, rcReply.Entries)
				srv.currentView = rcReply.View
				srv.lastNormalView =  srv.currentView
				srv.log = rcReply.Entries
				srv.commitIndex = rcReply.PrimaryCommit
				srv.status = NORMAL
				srv.mu.Unlock()
			}
		}(args.View)
		reply.Success = false
		srv.mu.Unlock()
	} else {
		// fmt.Printf("Index not found server %d, retry %d\n", srv.me, srv.prepareRetry+1)
		srv.prepareRetry = srv.prepareRetry + 1
		// Issue another prepare call, which will be processed later
		srv.mu.Unlock()
		srv.sendPrepare(srv.me, args, reply)
	}
}

// Recovery is the RPC handler for the Recovery RPC
func (srv *PBServer) Recovery(args *RecoveryArgs, reply *RecoveryReply) {
	// Your code here
	if srv.status == NORMAL {
		reply.View = srv.currentView
		reply.Entries = srv.log
		reply.PrimaryCommit = srv.commitIndex
		reply.Success = true
		// fmt.Printf("Recovery Handler server %d, view %d, commit %d\n", srv.me, srv.currentView, srv.commitIndex)
	} else {
		reply.Success = false
	}
}

// Some external oracle prompts the primary of the newView to
// switch to the newView.
// PromptViewChange just kicks start the view change protocol to move to the newView
// It does not block waiting for the view change process to complete.
func (srv *PBServer) PromptViewChange(newView int) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	newPrimary := GetPrimary(newView, len(srv.peers))

	if newPrimary != srv.me { //only primary of newView should do view change
		return
	} else if newView <= srv.currentView {
		return
	}
	vcArgs := &ViewChangeArgs{
		View: newView,
	}
	vcReplyChan := make(chan *ViewChangeReply, len(srv.peers))
	// send ViewChange to all servers including myself
	for i := 0; i < len(srv.peers); i++ {
		go func(server int) {
			var reply ViewChangeReply
			ok := srv.peers[server].Call("PBServer.ViewChange", vcArgs, &reply)
			// fmt.Printf("node-%d (nReplies %d) received reply ok=%v reply=%v\n", srv.me, nReplies, ok, r.reply)
			if ok {
				vcReplyChan <- &reply
			} else {
				vcReplyChan <- nil
			}
		}(i)
	}

	// wait to receive ViewChange replies
	// if view change succeeds, send StartView RPC
	go func() {
		var successReplies []*ViewChangeReply
		var nReplies int
		majority := len(srv.peers)/2 + 1
		for r := range vcReplyChan {
			nReplies++
			if r != nil && r.Success {
				successReplies = append(successReplies, r)
			}
			if nReplies == len(srv.peers) || len(successReplies) == majority {
				break
			}
		}
		ok, log := srv.determineNewViewLog(successReplies)
		if !ok {
			return
		}
		svArgs := &StartViewArgs{
			View: vcArgs.View,
			Log:  log,
		}
		// send StartView to all servers including myself
		for i := 0; i < len(srv.peers); i++ {
			var reply StartViewReply
			go func(server int) {
				// fmt.Printf("node-%d sending StartView v=%d to node-%d\n", srv.me, svArgs.View, server)
				srv.peers[server].Call("PBServer.StartView", svArgs, &reply)
			}(i)
		}
	}()
}

// determineNewViewLog is invoked to determine the log for the newView based on
// the collection of replies for successful ViewChange requests.
// if a quorum of successful replies exist, then ok is set to true.
// otherwise, ok = false.
func (srv *PBServer) determineNewViewLog(successReplies []*ViewChangeReply) (
	ok bool, newViewLog []interface{}) {
	// Your code here
	lastView := -1
	if len(successReplies) <= len(srv.peers)/2 {
		return false, newViewLog
	}
	for i := 0; i < len(successReplies); i++ {
		if successReplies[i].LastNormalView > lastView {
			lastView = successReplies[i].LastNormalView
			newViewLog = successReplies[i].Log
		} else if successReplies[i].LastNormalView == lastView {
			if len(successReplies[i].Log) > len(newViewLog) {
				newViewLog = successReplies[i].Log
			}
		}
	}
	// fmt.Printf("Server %d checkout the Last View %d with log %d\n", srv.me, lastView, len(newViewLog))
	ok = true
	return ok, newViewLog
}

// ViewChange is the RPC handler to process ViewChange RPC.
func (srv *PBServer) ViewChange(args *ViewChangeArgs, reply *ViewChangeReply) {
	// Your code here
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if(srv.currentView < args.View) {
		// srv.currentView = args.View
		srv.status = VIEWCHANGE
		reply.Success = true
		reply.Log = srv.log
		reply.LastNormalView = srv.lastNormalView
		// fmt.Printf("Viewchange Reply from %d: Log %d, Last view %d\n", srv.me, len(srv.log), srv.lastNormalView)
	} else {
		reply.Success = false
	}
}

// StartView is the RPC handler to process StartView RPC.
func (srv *PBServer) StartView(args *StartViewArgs, reply *StartViewReply) {
	// Your code here
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if(srv.currentView <= args.View) {
		srv.currentView = args.View
		srv.log = args.Log
		srv.lastNormalView = srv.currentView
		srv.status = NORMAL
		srv.commitIndex = len(srv.log)-1
		// fmt.Printf("Server %d starts view %d with log %v\n", srv.me, args.View, args.Log)
	}
}
