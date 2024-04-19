package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/cache/v8"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type Data interface {
	IsDestructive() bool
}

type Note struct {
	TextData	[]byte
	Attachment  bool
	Destruct	bool
}

type File struct {
	FileData	[]byte
	FileMetadata string
	Destruct	bool
}

type Server struct {
	BaseURL    string
	RedisCache *cache.Cache
}

func (n Note) IsDestructive() bool {
	return n.Destruct
}

func (f File) IsDestructive() bool {
	return f.Destruct
}

func main() {

	port := os.Getenv("PORT")
	if len(port) == 0 {
		port = "3000"
	}
	addr := ":" + port

	baseURL := os.Getenv("BASE_URL")
	if len(baseURL) == 0 {
		baseURL = fmt.Sprintf("http://localhost:%s", port)
	}

	redisURL := os.Getenv("REDIS_URL")
	if len(redisURL) == 0 {
		redisURL = "redis://:@localhost:6379/1"
	}

	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		panic(err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()
	redisCache := cache.New(&cache.Options{
		Redis: redisClient,
	})
	server := &Server{
		BaseURL:    baseURL,
		RedisCache: redisCache,
	}

    r := mux.NewRouter()
	r.StrictSlash(false)
	r.HandleFunc("/",server.handleGET).Methods("GET")
	r.HandleFunc("/{idx}",server.handleGET).Methods("GET")
	r.HandleFunc("/download/{idx}",server.handleDownload).Methods("GET")
	r.HandleFunc("/",server.handlePOST).Methods("POST")

	r.NotFoundHandler = http.HandlerFunc(server.notFound)
    
	log.Printf("Server started on %s \n",addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

func (s *Server) contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			contentType := r.Header.Get("Content-Type")
			if contentType != "application/x-www-form-urlencoded" {
				s.responseError(
					w, r,
					http.StatusUnsupportedMediaType,
					"Invalid media type posted.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) responseError( w http.ResponseWriter, r *http.Request, code int,msg string) {
		w.WriteHeader(code)
		s.renderMessage(
			w, r,
			"",
			template.HTML(
				fmt.Sprintf("<div>%s</div>", msg)))
} 

func (s *Server) notFound(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.responseError(w,r,http.StatusNotFound,"Not found")
}

func (s *Server) renderTemplate(
	w http.ResponseWriter,
	r *http.Request,
	data interface{},
	name string,
	files ...string,
) {
	t := template.Must(template.ParseFiles(files...))
	err := t.ExecuteTemplate(w, name, data)
	if err != nil {
		panic(err)
	}
}

func (s *Server) renderMessage(
	w http.ResponseWriter,
	r *http.Request,
	title string,
	paragraphs ...interface{},
) {
	s.renderTemplate(
		w, r,
		struct {
			Title      string
			Paragraphs []interface{}
		}{
			Title:      title,
			Paragraphs: paragraphs,
		},
		"layout",
		"dist/layout.html",
		"dist/message.html",
	)
}

func (s *Server) handlePOST(
	w http.ResponseWriter,
	r *http.Request,
) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		s.responseError(
			w, r,
			http.StatusBadRequest,
			"Invalid form data posted.")
		return
	}
	
	attachment, handler, err := r.FormFile("fileInput")
	if err != nil {
		if err != http.ErrMissingFile {
			s.responseError(w,r,http.StatusBadRequest, "Unable to get file from form")
			return
		}
	}

	form := r.PostForm
	message := form.Get("message")
	destruct := false
	ttl := time.Hour * 24
	if form.Get("ttl") == "untilRead" {
		destruct = true
		ttl = ttl * 365
	}

	note := &Note{
		TextData: []byte(message),
		Destruct: destruct,
	}


	key := uuid.NewString()

	if attachment != nil {
		defer attachment.Close()

		note.Attachment = true

		f, err := io.ReadAll(attachment)
		if err != nil {
			s.responseError(w,r,http.StatusBadRequest, "Something went wrong reading the file bytes")
			return
		}	

		file := &File{
			FileData: f,
			FileMetadata: handler.Filename,
			Destruct: destruct,
		}

		err = s.RedisCache.Set(
			&cache.Item{
				Ctx:            r.Context(),
				Key:            "file_" + key,
				Value:          file,
				TTL:            ttl,
				SkipLocalCache: true,
			})
		if err != nil {
			log.Println(err.Error())
			s.responseError(w, r,http.StatusInternalServerError,err.Error())
			return
		}
		
	}

	err = s.RedisCache.Set(
		&cache.Item{
			Ctx:            r.Context(),
			Key:            key,
			Value:          note,
			TTL:            ttl,
			SkipLocalCache: true,
		})
	if err != nil {
		log.Println(err.Error())
		s.responseError(w, r,http.StatusInternalServerError,err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	noteURL := fmt.Sprintf("%s/%s", s.BaseURL, key)


	data := map[string]string{
		"Share via Email":    fmt.Sprintf("mailto:?subject=Secure%%20Link&body=%s",noteURL),
		"Share via Facebook": fmt.Sprintf("https://www.facebook.com/sharer/sharer.php?u=%s",noteURL),
		"Share via Twitter":  fmt.Sprintf("https://twitter.com/intent/tweet?url=%s&text=Secure%%20Link",noteURL),
		"Share via WhatsApp": fmt.Sprintf("https://api.whatsapp.com/send?text=Secure%%20Link:%%20%s",noteURL),
		"Share via Telegram": fmt.Sprintf("https://t.me/share/url?url=%s&text=Secure%%20Link",noteURL),
	}

	htmlShareLinks := fmt.Sprintf("<a href='%s'>%s</a>", noteURL, noteURL)
	for t, l := range data{
      htmlShareLinks = htmlShareLinks + fmt.Sprintf("<p><a href='%s' target='_blank'>%s</a></p>",l,t)
	}

	s.renderMessage(
		w, r,
		"Note was successfully created",
		template.HTML(htmlShareLinks))
}

func (s *Server) handleGET(
	w http.ResponseWriter,
	r *http.Request,
) {
	path := r.URL.Path
	if path == "/" {
		s.renderTemplate(
			w, r, nil,
			"layout",
			"dist/layout.html",
			"dist/index.html")
		return
	}

	vars := mux.Vars(r)
	ctx := r.Context()
	noteID := vars["idx"]
	note := &Note{}
	err := getData(s,ctx,noteID,note)

	if err != nil {
		s.responseError(
			w, r,
			http.StatusInternalServerError,
			err.Error())
		return
	}

	var t string

	if (note.Attachment) {
		t = fmt.Sprintf("<div><div>%s</div><p></p><div><a href=/download/%s>Attachment &#128206;</a></div></div>", note.TextData,noteID)
	}else {
		t = fmt.Sprintf("<div><div>%s</div></div>", note.TextData)
	}

	w.WriteHeader(http.StatusOK)
	s.renderMessage(
		w, r,
		"Note",
		template.HTML(t))
}


func (s *Server) handleDownload(
	w http.ResponseWriter,
	r *http.Request,
) {

	vars := mux.Vars(r)
	ctx := r.Context()
	noteID := "file_" + vars["idx"]
	file := &File{}

	err := getData(s,ctx,noteID,file)

	if err != nil {
		s.responseError(
			w, r,
			http.StatusInternalServerError,
			err.Error())
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+file.FileMetadata)
	w.Header().Set("Content-Type", http.DetectContentType(file.FileData))
	w.Write(file.FileData)
}

func getData(s *Server, ctx context.Context, noteID string, data Data) (error) {

	err := s.RedisCache.GetSkippingLocalCache(
		ctx,
		noteID,
		&data)
	if err != nil {
		return errors.New(http.StatusText(404))
	}

	switch v := data.(type) {
	case *Note, *File:
		if v.IsDestructive() {
			err := s.RedisCache.Delete(ctx, noteID)
			if err != nil {
				return errors.New(http.StatusText(500))
			}
		}
	default:
		return errors.New(http.StatusText(500))
	}

	return nil
}