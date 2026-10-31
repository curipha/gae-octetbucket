package main

import (
  "bytes"
  "context"
  "crypto/sha256"
  "errors"
  "fmt"
  "hash/crc32"
  "io"
  "log"
  "mime"
  "net/http"
  "os"
  "path"
  "strings"
  "time"

  "cloud.google.com/go/datastore"
  "google.golang.org/grpc/codes"
  "google.golang.org/grpc/status"
)

var projectid = os.Getenv("GOOGLE_CLOUD_PROJECT")

var client *datastore.Client

const kind    = "Storage"
const formarg = "file"

const getpoint = "r"

const keylen = 12 // hex chars (= 6 bytes of SHA-256)
const maxbodysize = 1024 * 1024 // 1MiB

type Storage struct {
  Created     time.Time
  RemoteAddr  string
  UserAgent   string `datastore:",noindex"`
  FileName    string
  ContentType string
  Size        int
  Data        []byte `datastore:",noindex"`
}

func remoteaddr(r *http.Request) string {
  addr := r.Header.Get("X-Appengine-User-Ip")
  if addr == "" {
    log.Println("'X-Appengine-User-Ip' header is empty! Get remote address from 'X-Forwarded-For'.")
    addr = strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]
  }

  return addr
}

func validkey(key string) bool {
  if len(key) != keylen {
    return false
  }
  for _, c := range key {
    if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
      return false
    }
  }
  return true
}

func getdel(w http.ResponseWriter, r *http.Request) {
  file := r.PathValue("file")
  ext := path.Ext(file)
  key := file[:len(file) - len(ext)]

  if !validkey(key) {
    http.NotFound(w, r)
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 10 * time.Second)
  defer cancel()

  record := &Storage{}
  dskey := datastore.NameKey(kind, key, nil)

  err := client.Get(ctx, dskey, record)
  if errors.Is(err, datastore.ErrNoSuchEntity) {
    http.NotFound(w, r)
    return
  }
  if err != nil {
    log.Printf("Failed to get the file from Datastore: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  switch r.Method {
  case http.MethodGet, http.MethodHead:
    ctype := mime.TypeByExtension(ext)

    switch strings.SplitN(ctype, "/", 2)[0] {
    case "text":
      ctype = "text/plain"
    case "audio", "video", "image":
      break // kept as it is
    default:
      ctype = "application/octet-stream"
    }

    w.Header().Set("Content-Type", ctype)
    w.Header().Set("Cache-Control", "public, max-age=300")
    w.Header().Set("Content-Security-Policy", "sandbox")
    w.Header().Set("X-Content-Type-Options", "nosniff")
    w.Header().Set("ETag", fmt.Sprintf(`"%s-%08x"`, key, crc32.ChecksumIEEE([]byte(ctype))))
    http.ServeContent(w, r, "", record.Created, bytes.NewReader(record.Data))
    return

  case http.MethodDelete:
    if record.RemoteAddr == remoteaddr(r) {
      err = client.Delete(ctx, dskey)
      if err == nil {
        log.Printf(
          "File deleted. Created: %s, RemoteAddr: %s, UserAgent: %s, FileName: %s, ContentType: %s, Size: %d",
          record.Created.Format(time.RFC3339),
          record.RemoteAddr,
          record.UserAgent,
          record.FileName,
          record.ContentType,
          record.Size,
        )

        w.WriteHeader(http.StatusNoContent)
        return
      } else {
        log.Printf("Failed to delete the file from Datastore: %v", err)
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
      }
    } else {
      http.Error(w, "Forbidden", http.StatusForbidden)
      return
    }
  }
}

func post(w http.ResponseWriter, r *http.Request) {
  r.Body = http.MaxBytesReader(w, r.Body, maxbodysize)

  file, header, err := r.FormFile(formarg)
  if err != nil {
    var maxerr *http.MaxBytesError
    if errors.As(err, &maxerr) {
      http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
      return
    }

    log.Printf("Failed to parse a uploaded file: %v", err)
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }
  defer file.Close()

  blob, err := io.ReadAll(file)
  if err != nil {
    log.Printf("Failed to read a uploaded file: %v", err)
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }
  if len(blob) == 0 {
    http.Error(w, "Bad Request: empty file", http.StatusBadRequest)
    return
  }

  fname := strings.TrimSpace(header.Filename)
  ctype := strings.TrimSpace(header.Header.Get("Content-Type"))

  key := fmt.Sprintf("%.*x", keylen / 2, sha256.Sum256(blob))

  ctx, cancel := context.WithTimeout(r.Context(), 10 * time.Second)
  defer cancel()

  record := &Storage {
    Created:     time.Now(),
    RemoteAddr:  remoteaddr(r),
    UserAgent:   strings.TrimSpace(r.Header.Get("User-Agent")),
    FileName:    fname,
    ContentType: ctype,
    Size:        len(blob),
    Data:        blob,
  }

  // Insert fails with AlreadyExists if the same content is already stored
  dskey := datastore.NameKey(kind, key, nil)
  _, err = client.Mutate(ctx, datastore.NewInsert(dskey, record))
  if err != nil && status.Code(err) != codes.AlreadyExists {
    log.Printf("Failed to store the file to Datastore: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  fmt.Fprintf(w, "https://%s/%s/%s%s\n", r.Host, getpoint, key, path.Ext(fname))
}

func main() {
  var err error
  log.Printf("Initialize Datastore client for project: %s", projectid)
  client, err = datastore.NewClient(context.Background(), projectid)
  if err != nil {
    log.Fatalf("Datastore client initialization failed: %v", err)
  }
  defer client.Close()

  mux := http.NewServeMux()
  mux.HandleFunc("GET /" + getpoint + "/{file}", getdel) // also matches HEAD
  mux.HandleFunc("DELETE /" + getpoint + "/{file}", getdel)
  mux.HandleFunc("POST /upload", post)

  port := os.Getenv("PORT")
  if port == "" {
    port = "8080"
    log.Printf("Defaulting to port %s", port)
  }

  server := &http.Server{
    Addr:              ":" + port,
    Handler:           mux,
    ReadHeaderTimeout: 10 * time.Second, // Mitigate Slowloris
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       60 * time.Second,
  }

  log.Printf("Listening on port %s", port)
  err = server.ListenAndServe()
  if err != nil {
    log.Fatal(err)
  }
}
