package stati

import (
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
)

var db *sql.DB
var templates = template.Must(template.ParseGlob("templates/*.html"))

type Event struct {
	ID           string
	Title        string
	Description  string
	DateTime     string
	UserResponse string
}

type Group struct {
	ID          int
	Title       string
	Description string
	CreatorID   int
	IsCreator   bool
	IsMember    bool
	IsRequested bool
	IsInvited   bool
}

type JoinRequest struct {
	UserID   int
	Username string
	Status   string
}

type Member struct {
	Username string
	Role     string
}

type InvitableUser struct {
	UserID   int
	Username string
	Invited  bool
}

type GroupPost struct {
	ID        int
	GroupID   int
	UserID    int
	Content   string
	Username  string
	CreatedAt string
	Comments  []GroupComment
}

type GroupComment struct {
	ID        int
	PostID    int
	UserID    int
	Content   string
	Username  string
	CreatedAt string
}

func InitHandlers(database *sql.DB) {
	db = database
}

func CreateGroupHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/homepage", http.StatusSeeOther)
		return
	}

	err = r.ParseForm()
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	if title == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Insert group
	res, err := tx.Exec(`INSERT INTO groups (title, description, creator_id) VALUES (?, ?, ?)`,
		title, description, userID,
	)
	if err != nil {
		log.Println("Insert group error:", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	groupID, err := res.LastInsertId()
	if err != nil {
		log.Println("Get last insert ID error:", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Add creator to group_members as admin
	_, err = tx.Exec(`
        INSERT INTO group_members (group_id, user_id, role, status)
        VALUES (?, ?, 'admin', 'accepted')`,
		groupID, userID,
	)
	if err != nil {
		log.Println("Insert group member error:", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("Transaction commit error:", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/homepage", http.StatusSeeOther)
}

func JoinGroupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/homepage", http.StatusSeeOther)
		return
	}

	// Validate session
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var userID int
	var expiry time.Time

	err = db.QueryRow("SELECT user_id, expiry FROM sessions WHERE session_id = ?", cookie.Value).Scan(&userID, &expiry)
	if err != nil || time.Now().After(expiry) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	groupID := r.FormValue("group_id")
	if groupID == "" {
		http.Error(w, "Group ID is required", http.StatusBadRequest)
		return
	}

	// Check if already a member or request exists
	var exists bool
	err = db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM group_members WHERE group_id = ? AND user_id = ?
		)`, groupID, userID).Scan(&exists)

	if err != nil {
		log.Println("Error checking group membership:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if exists {
		http.Redirect(w, r, "/homepage", http.StatusSeeOther)
		return
	}

	// Add the user as a 'requested' member (awaiting admin approval)
	_, err = db.Exec(`
	INSERT INTO group_members (group_id, user_id, role, status)
	VALUES (?, ?, 'member', 'requested')
	`, groupID, userID)

	if err != nil {
		log.Println("Failed to insert group member:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/homepage", http.StatusSeeOther)
}
func GetGroups(userID int) ([]Group, error) {
	rows, err := db.Query(`
        SELECT g.id, g.title, g.description,
               CASE WHEN g.creator_id = ? THEN 1 ELSE 0 END AS is_creator,
               CASE WHEN gm.user_id IS NOT NULL AND gm.status = 'accepted' THEN 1 ELSE 0 END AS is_member,
               CASE WHEN gm.user_id IS NOT NULL AND gm.status = 'requested' THEN 1 ELSE 0 END AS is_requested,
               CASE WHEN gm.user_id IS NOT NULL AND gm.status = 'invited' THEN 1 ELSE 0 END AS is_invited
        FROM groups g
        LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = ?
    `, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		var isCreatorInt, isMemberInt, isRequestedInt, isInvitedInt int
		err := rows.Scan(&g.ID, &g.Title, &g.Description, &isCreatorInt, &isMemberInt, &isRequestedInt, &isInvitedInt)
		if err != nil {
			return nil, err
		}
		g.IsCreator = isCreatorInt == 1
		g.IsMember = isMemberInt == 1
		g.IsRequested = isRequestedInt == 1
		g.IsInvited = isInvitedInt == 1
		groups = append(groups, g)
	}
	return groups, nil
}

func GroupPageHandler(w http.ResponseWriter, r *http.Request) {
	// Step 1: Get user ID from cookie
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	// Step 2: Get group ID from URL
	groupIDStr := r.URL.Query().Get("id")
	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil {
		http.Error(w, "Invalid group ID", http.StatusBadRequest)
		return
	}

	// Step 3: Fetch group info
	var group Group
	err = db.QueryRow(`SELECT id, title, description, creator_id FROM groups WHERE id = ?`, groupID).
		Scan(&group.ID, &group.Title, &group.Description, &group.CreatorID)
	if err != nil {
		http.Error(w, "Group not found", http.StatusNotFound)
		return
	}
	group.IsCreator = group.CreatorID == userID

	// Step 4: Check if user is accepted member
	var exists int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM group_members 
		WHERE group_id = ? AND user_id = ? AND status = 'accepted'
	`, groupID, userID).Scan(&exists)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	group.IsMember = exists > 0

	// Step 5: Only allow access if creator or member
	if !group.IsCreator && !group.IsMember {
		http.Error(w, "You are not a member of this group", http.StatusForbidden)
		return
	}

	// Step 6a: Fetch accepted group members
	rows, err := db.Query(`
		SELECT u.username, gm.role
		FROM users u
		JOIN group_members gm ON u.id = gm.user_id
		WHERE gm.group_id = ? AND gm.status = 'accepted'
	`, groupID)
	if err != nil {
		http.Error(w, "Unable to fetch members", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		err := rows.Scan(&m.Username, &m.Role)
		if err != nil {
			http.Error(w, "Error reading members", http.StatusInternalServerError)
			return
		}
		members = append(members, m)
	}

	// Step 6b: Fetch join requests (only for group creator)
	var requestedMembers []JoinRequest
	if group.IsCreator {
		reqRows, err := db.Query(`
			SELECT u.id, u.username, gm.status
			FROM users u
			JOIN group_members gm ON u.id = gm.user_id
			WHERE gm.group_id = ? AND gm.status = 'requested'
		`, groupID)
		if err != nil {
			http.Error(w, "Unable to fetch join requests", http.StatusInternalServerError)
			return
		}
		defer reqRows.Close()

		for reqRows.Next() {
			var jr JoinRequest
			err := reqRows.Scan(&jr.UserID, &jr.Username, &jr.Status)
			if err != nil {
				http.Error(w, "Error reading join requests", http.StatusInternalServerError)
				return
			}
			requestedMembers = append(requestedMembers, jr)
		}
	}

	// Step 6c: Fetch invitable users (only for group creator)
	var invitableUsers []InvitableUser
	if group.IsCreator {
		inviteRows, err := db.Query(`
			SELECT u.id, u.username,
			CASE WHEN gm.status IS NOT NULL THEN 1 ELSE 0 END AS invited
			FROM users u
			LEFT JOIN group_members gm ON gm.user_id = u.id AND gm.group_id = ?
			WHERE u.id != ? 
			  AND (gm.status IS NULL OR gm.status NOT IN ('accepted'))
		`, group.ID, userID)

		if err != nil {
			http.Error(w, "Unable to fetch invitable users", http.StatusInternalServerError)
			return
		}
		defer inviteRows.Close()

		for inviteRows.Next() {
			var u InvitableUser
			var invitedInt int
			err := inviteRows.Scan(&u.UserID, &u.Username, &invitedInt)
			if err != nil {
				http.Error(w, "Error reading invitable users", http.StatusInternalServerError)
				return
			}
			u.Invited = invitedInt == 1
			invitableUsers = append(invitableUsers, u)
		}
	}

	// Step 7: Fetch group events and RSVP responses
	var events []Event
	eventRows, err := db.Query(`
		SELECT id, title, description, datetime 
		FROM group_events 
		WHERE group_id = ? 
		ORDER BY datetime ASC
	`, groupID)
	if err != nil {
		http.Error(w, "Unable to fetch group events", http.StatusInternalServerError)
		return
	}
	defer eventRows.Close()

	for eventRows.Next() {
		var ev Event
		err := eventRows.Scan(&ev.ID, &ev.Title, &ev.Description, &ev.DateTime)
		if err != nil {
			http.Error(w, "Error reading events", http.StatusInternalServerError)
			return
		}

		// Get user's response
		err = db.QueryRow(`
			SELECT response FROM event_responses 
			WHERE event_id = ? AND user_id = ?
		`, ev.ID, userID).Scan(&ev.UserResponse)
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Error fetching event responses", http.StatusInternalServerError)
			return
		}

		events = append(events, ev)
	}

	// Step 8: Fetch group posts and comments (FIXED)
	var posts []GroupPost
	postRows, err := db.Query(`
		SELECT p.id, p.group_id, p.user_id, p.content, p.created_at, u.username
		FROM group_posts p
		JOIN users u ON p.user_id = u.id
		WHERE p.group_id = ?
		ORDER BY p.created_at DESC
	`, groupID)
	if err != nil {
		log.Println("Failed to load posts:", err) // Add logging
		http.Error(w, "Failed to load posts", http.StatusInternalServerError)
		return
	}
	defer postRows.Close()

	for postRows.Next() {
		var post GroupPost
		err := postRows.Scan(&post.ID, &post.GroupID, &post.UserID, &post.Content, &post.CreatedAt, &post.Username)
		if err != nil {
			log.Println("Failed to read post:", err) // Add logging
			http.Error(w, "Failed to read post", http.StatusInternalServerError)
			return
		}

		// Fetch comments for each post
		commentRows, err := db.Query(`
			SELECT c.id, c.post_id, c.user_id, c.content, c.created_at, u.username
			FROM group_comments c
			JOIN users u ON c.user_id = u.id
			WHERE c.post_id = ?
			ORDER BY c.created_at ASC
		`, post.ID)
		if err != nil {
			log.Println("Failed to load comments:", err) // Add logging
			http.Error(w, "Failed to load comments", http.StatusInternalServerError)
			return
		}

		for commentRows.Next() {
			var comment GroupComment
			err := commentRows.Scan(&comment.ID, &comment.PostID, &comment.UserID, &comment.Content, &comment.CreatedAt, &comment.Username)
			if err != nil {
				log.Println("Failed to read comment:", err) // Add logging
				http.Error(w, "Failed to read comment", http.StatusInternalServerError)
				return
			}
			post.Comments = append(post.Comments, comment)
		}
		commentRows.Close() // Close inside the loop

		posts = append(posts, post)
	}

	// Step 9: Render template with all group data
	data := struct {
		Group            Group
		Members          []Member
		IsAdmin          bool
		RequestedMembers []JoinRequest
		InvitableUsers   []InvitableUser
		Events           []Event
		Posts            []GroupPost
	}{
		Group:            group,
		Members:          members,
		IsAdmin:          group.IsCreator,
		RequestedMembers: requestedMembers,
		InvitableUsers:   invitableUsers,
		Events:           events,
		Posts:            posts,
	}

	templates.ExecuteTemplate(w, "group.html", data)
}


func AcceptJoinRequestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user ID from session cookie
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var adminID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&adminID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	// Parse form data
	err = r.ParseForm()
	if err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := r.FormValue("user_id")
	groupID := r.FormValue("group_id")

	// Check if requester is the group creator
	var creatorID int
	err = db.QueryRow(`SELECT creator_id FROM groups WHERE id = ?`, groupID).Scan(&creatorID)
	if err != nil || creatorID != adminID {
		http.Error(w, "You are not authorized", http.StatusForbidden)
		return
	}

	// Update status to 'accepted'
	_, err = db.Exec(`UPDATE group_members SET status = 'accepted' WHERE group_id = ? AND user_id = ?`, groupID, userID)
	if err != nil {
		http.Error(w, "Failed to accept request", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/group?id="+groupID, http.StatusSeeOther)
}
func RejectJoinRequestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user ID from session cookie
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var adminID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&adminID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	// Parse form data
	err = r.ParseForm()
	if err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := r.FormValue("user_id")
	groupID := r.FormValue("group_id")

	// Check if requester is the group creator
	var creatorID int
	err = db.QueryRow(`SELECT creator_id FROM groups WHERE id = ?`, groupID).Scan(&creatorID)
	if err != nil || creatorID != adminID {
		http.Error(w, "You are not authorized", http.StatusForbidden)
		return
	}

	// Delete the join request
	_, err = db.Exec(`DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID)
	if err != nil {
		http.Error(w, "Failed to reject request", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/group?id="+groupID, http.StatusSeeOther)
}
func GroupInviteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var adminID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&adminID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	err = r.ParseForm()
	if err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := r.FormValue("user_id")
	groupID := r.FormValue("group_id")

	// Verify admin is group creator
	var creatorID int
	err = db.QueryRow(`SELECT creator_id FROM groups WHERE id = ?`, groupID).Scan(&creatorID)
	if err != nil || creatorID != adminID {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	// Add invited user as invited member (pending acceptance)
	_, err = db.Exec(`
	INSERT INTO group_members (group_id, user_id, role, status)
	VALUES (?, ?, 'member', 'invited')`, groupID, userID)

	if err != nil {
		http.Error(w, "Failed to invite user", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/group?id="+groupID, http.StatusSeeOther)
}

func AcceptInviteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	groupIDStr := r.FormValue("group_id")
	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil {
		http.Error(w, "Invalid group ID", http.StatusBadRequest)
		return
	}

	// Update status from "invited" to "accepted"
	_, err = db.Exec(`UPDATE group_members SET status = 'accepted' WHERE group_id = ? AND user_id = ? AND status = 'invited'`, groupID, userID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/homepage", http.StatusSeeOther)
}

func RejectInviteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}
	groupIDStr := r.FormValue("group_id")
	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil {
		http.Error(w, "Invalid group ID", http.StatusBadRequest)
		return
	}

	// Remove the invitation - delete the row where status = 'invited'
	_, err = db.Exec(`DELETE FROM group_members WHERE group_id = ? AND user_id = ? AND status = 'invited'`, groupID, userID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/homepage", http.StatusSeeOther)
}

func CreateEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	groupIDStr := r.FormValue("group_id")
	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil {
		http.Error(w, "Invalid group ID", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	datetime := r.FormValue("datetime")

	_, err = db.Exec(`
        INSERT INTO group_events (group_id, title, description, datetime, created_by)
        VALUES (?, ?, ?, ?, ?)`,
		groupID, title, description, datetime, userID,
	)
	if err != nil {
		http.Error(w, "Could not create event", http.StatusInternalServerError)
		log.Println("Insert error:", err) // optional: useful for debugging
		return
	}

	http.Redirect(w, r, "/group?id="+groupIDStr, http.StatusSeeOther)
}

func EventResponseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	eventID := r.FormValue("event_id")
	response := r.FormValue("response")

	_, err = db.Exec(`
        INSERT INTO event_responses (event_id, user_id, response)
        VALUES (?, ?, ?)
        ON CONFLICT(event_id, user_id) DO UPDATE SET response=excluded.response, responded_at=CURRENT_TIMESTAMP
    `, eventID, userID, response)

	if err != nil {
		http.Error(w, "Could not save response", http.StatusInternalServerError)
		log.Println("Event response insert error:", err)
		return
	}

	// Redirect back to the group page
	var groupID string
	err = db.QueryRow("SELECT group_id FROM group_events WHERE id = ?", eventID).Scan(&groupID)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	http.Redirect(w, r, "/group?id="+groupID, http.StatusSeeOther)
}

func CreateGroupPostHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form data", http.StatusBadRequest)
		return
	}

	groupID, _ := strconv.Atoi(r.FormValue("group_id"))
	content := r.FormValue("content")

	_, err = db.Exec(`INSERT INTO group_posts (group_id, user_id, content) VALUES (?, ?, ?)`, groupID, userID, content)
	if err != nil {
		http.Error(w, "Unable to create post", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/group?id="+r.FormValue("group_id"), http.StatusSeeOther)
}

func CreateGroupCommentHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var userID int
	err = db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID)
	if err != nil {
		http.Error(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form data", http.StatusBadRequest)
		return
	}

	postID, _ := strconv.Atoi(r.FormValue("post_id"))
	groupID := r.FormValue("group_id")
	content := r.FormValue("content")

	_, err = db.Exec(`INSERT INTO group_comments (post_id, user_id, content) VALUES (?, ?, ?)`, postID, userID, content)
	if err != nil {
		http.Error(w, "Unable to create comment", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/group?id="+groupID, http.StatusSeeOther)
}
