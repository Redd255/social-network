package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	stati "social/src"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var templates = template.Must(template.ParseGlob("templates/*.html"))

func init() {
	idb, err := sql.Open("sqlite3", "./database/my.db")
	if err != nil {
		log.Fatal(err)
	}
	db = idb
	stati.InitHandlers(db)

	sqlfile, err := os.ReadFile("./database/my.sql")
	if err != nil {
		log.Fatal("Failed to read SQL file:", err)
	}
	_, err = db.Exec(string(sqlfile))
	if err != nil {
		log.Fatal("Failed to create users table:", err)
	}
}
func main() {
	staticDir := filepath.Join(".", "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	http.HandleFunc("/", login)
	http.HandleFunc("/register", register) // done
	http.HandleFunc("/logout", logout) // done
	
	http.HandleFunc("/homepage", homepage) // done homepage
	http.HandleFunc("/create-group", stati.CreateGroupHandler) // done
	http.HandleFunc("/group/", stati.GroupPageHandler) // bach dkhel
	http.HandleFunc("/join-group", stati.JoinGroupHandler) 
	http.HandleFunc("/group/accept", stati.AcceptJoinRequestHandler)
	http.HandleFunc("/group/reject", stati.RejectJoinRequestHandler)
	http.HandleFunc("/group/invite", stati.GroupInviteHandler)
	http.HandleFunc("/group/accept-invite", stati.AcceptInviteHandler)
	http.HandleFunc("/group/reject-invite", stati.RejectInviteHandler)
	http.HandleFunc("/group/create-event", stati.CreateEventHandler)
	http.HandleFunc("/event/respond", stati.EventResponseHandler)
	http.HandleFunc("/group/create-post", stati.CreateGroupPostHandler)
	http.HandleFunc("/group/create-comment", stati.CreateGroupCommentHandler)

	fmt.Println("Server running at http://localhost:8980")
	log.Fatal(http.ListenAndServe(":8980", nil))
}

// register handles user registration
func register(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		templates.ExecuteTemplate(w, "register.html", nil)
		return
	}

	username := r.FormValue("username")
	email := r.FormValue("email")
	password := r.FormValue("password")
	nickname := r.FormValue("nickname")
	ageStr := r.FormValue("age")
	gender := r.FormValue("gender")
	firstName := r.FormValue("first_name")
	lastName := r.FormValue("last_name")

	if username == "" || email == "" || password == "" {
		errorPage(w, "Username, Email and Password are required", "register.html")
		return
	}

	// Parse age if provided
	var age int
	if ageStr != "" {
		var err error
		age, err = strconv.Atoi(ageStr)
		if err != nil {
			errorPage(w, "Age must be a valid number", "register.html")
			return
		}
	}

	//check email
	var existingEmail string
	err := db.QueryRow("SELECT email FROM users WHERE email = ?", email).Scan(&existingEmail)
	if err == nil {
		errorPage(w, "Email already in use", "register.html")
		return
		// if any other err expt norows
	} else if err != sql.ErrNoRows {
		log.Println("Database error:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	//check username
	var existingUserName string
	err = db.QueryRow("SELECT username FROM users WHERE username = ?", username).Scan(&existingUserName)
	if err == nil {
		errorPage(w, "UserName already in use", "register.html")
		return
	} else if err != sql.ErrNoRows {
		log.Println("Database error:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Store password as plain text (no hashing)
	_, err = db.Exec(`
		INSERT INTO users (
			username, email, password, 
			nickname, age, gender, 
			first_name, last_name
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		username, email, password,
		nickname, age, gender,
		firstName, lastName)

	if err != nil {
		log.Println("Failed to insert user:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// login handles user authentication
func login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		templates.ExecuteTemplate(w, "login.html", nil)
		return
	}

	usernameOrEmail := r.FormValue("username")
	password := r.FormValue("password")

	if usernameOrEmail == "" || password == "" {
		errorPage(w, "Username/Email and password are required", "login.html")
		return
	}

	var userID int
	var storedPassword string
	err := db.QueryRow("SELECT id, password FROM users WHERE username = ? OR email = ?", usernameOrEmail, usernameOrEmail).Scan(&userID, &storedPassword)

	if err == sql.ErrNoRows {
		errorPage(w, "Invalid username/email or password", "login.html")
		return
	} else if err != nil {
		log.Printf("Database error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Simple string comparison instead of bcrypt
	if password != storedPassword {
		errorPage(w, "Invalid username or password", "login.html")
		return
	}

	// delete old sessions
	_, err = db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	if err != nil {
		log.Printf("Failed to clear old sessions: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// create a session
	sessionUUID, err := uuid.NewV4()
	if err != nil {
		log.Printf("Failed to generate UUID: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	sessionID := sessionUUID.String()

	// expiry date 24h
	expiry := time.Now().Add(24 * time.Hour)
	_, err = db.Exec("INSERT INTO sessions (session_id, user_id, expiry) VALUES (?, ?, ?)",
		sessionID, userID, expiry)
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Expires:  expiry,
		Path:     "/",
		HttpOnly: true,
	})

	http.Redirect(w, r, "/homepage", http.StatusSeeOther)
}

// Logout handles user logout
func logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		_, err := db.Exec("DELETE FROM sessions WHERE session_id = ?", cookie.Value)
		if err != nil {
			log.Printf("Failed to delete session: %v", err)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func errorPage(w http.ResponseWriter, message, templateName string) {
	data := map[string]string{
		"ErrorMessage": message,
	}
	err := templates.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, "Failed to execute template: "+err.Error(), http.StatusInternalServerError)
	}
}

// HomePage handles post creation and display
func homepage(w http.ResponseWriter, r *http.Request) {
	//check user session
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Validate session
	var userID int
	var username string
	var expiry time.Time

	err = db.QueryRow(`SELECT user_id, expiry FROM sessions WHERE session_id = ?`, cookie.Value).Scan(&userID, &expiry)
	if err != nil || time.Now().After(expiry) {
		if err == nil {
			db.Exec("DELETE FROM sessions WHERE session_id = ?", cookie.Value)
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	db.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username)

	groups, _ := stati.GetGroups(userID)

	data := struct {
		Username string
		UserID   int

		Groups []stati.Group
	}{
		Username: username,
		UserID:   userID,
		Groups:   groups,
	}

	templates.ExecuteTemplate(w, "homepage.html", data)
}
