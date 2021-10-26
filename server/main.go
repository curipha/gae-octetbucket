package main

import (
  "context"
  "crypto/sha256"
  "fmt"
  "io/ioutil"
  "log"
  "mime"
  "net/http"
  "os"
  "path"
  "strings"
  "time"

  "cloud.google.com/go/datastore"
  "google.golang.org/api/iterator"
)

var projectid = os.Getenv("GOOGLE_CLOUD_PROJECT")

const kind    = "Storage"
const formarg = "file"

const getpoint = "r"

type Storage struct {
  Created     time.Time
  RemoteAddr  string
  UserAgent   string
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

func keyexist(ctx context.Context, client *datastore.Client, key *datastore.Key) bool {
  query := datastore.NewQuery(kind).Filter("__key__ =", key).Limit(1).KeysOnly()

  _, err := client.Run(ctx, query).Next(nil)
  return err != iterator.Done
}

func getdel(w http.ResponseWriter, r *http.Request, method string) {
  dir, file := path.Split(r.URL.Path)
  if strings.Trim(dir, "/") != getpoint {
    http.NotFound(w, r)
    return
  }
  if file == "" {
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  ext := path.Ext(file)
  key := file[:len(file) - len(ext)]


  ctx := context.Background()
  client, err := datastore.NewClient(ctx, projectid)
  if err != nil {
    log.Printf("Failed to create client: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  // Check the existence firstly to reduce the cost (Key-only query is free!!)
  dskey := datastore.NameKey(kind, key, nil)
  if !keyexist(ctx, client, dskey) {
    http.NotFound(w, r)
    return
  }


  record := &Storage{}

  err = client.Get(ctx, dskey, record)
  if err != nil {
    log.Printf("Failed to get the file from Datastore: %v", err)
    http.NotFound(w, r)
    return
  }

  switch method {
  case "GET":
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
    w.Write(record.Data)
    return

  case "DELETE":
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

        // This is not an error.
        // Use http.Error here to just send the HTTP Status code and informational text back to users.
        http.Error(w, "Accepted", http.StatusAccepted)
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
  file, header, err := r.FormFile(formarg)
  if err != nil {
    log.Printf("Failed to parse a uploaded file: %v", err)
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  blob, err := ioutil.ReadAll(file)
  if err != nil {
    log.Printf("Failed to read a uploaded file: %v", err)
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  fname := strings.TrimSpace(header.Filename)
  ctype := strings.TrimSpace(header.Header.Get("Content-Type"))

  key := fmt.Sprintf("%.6x", sha256.Sum256(blob)) // 12 chars


  ctx := context.Background()
  client, err := datastore.NewClient(ctx, projectid)
  if err != nil {
    log.Printf("Failed to create client: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  dskey := datastore.NameKey(kind, key, nil)
  if !keyexist(ctx, client, dskey) {
    // Put new record iff no data exists
    record := &Storage {
      Created:     time.Now(),
      RemoteAddr:  remoteaddr(r),
      UserAgent:   strings.TrimSpace(r.Header.Get("User-Agent")),
      FileName:    fname,
      ContentType: ctype,
      Size:        len(blob),
      Data:        blob,
    }

    _, err = client.Put(ctx, dskey, record)
    if err != nil {
      log.Printf("Failed to store the file to Datastore: %v", err)
      http.Error(w, "Bad Request", http.StatusBadRequest)
      return
    }
  }

  ext := path.Ext(fname)
  if ext == "" {
    exts, err := mime.ExtensionsByType(ctype)
    if err == nil && len(exts) > 0 {
      ext = exts[0]
    }
  }

  fmt.Fprintf(w, "https://%s/%s/%s%s\n", r.Host, getpoint, key, ext)
}

func main() {
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case "GET", "DELETE":
      getdel(w, r, r.Method)
    case "POST":
      post(w, r)
    default:
      http.Error(w, "Not Implemented", http.StatusNotImplemented)
    }
  })


  port := os.Getenv("PORT")
  if port == "" {
    port = "8080"
    log.Printf("Defaulting to port %s", port)
  }

  log.Printf("Listening on port %s", port)
  err := http.ListenAndServe(":" + port, nil)
  if err != nil {
    log.Fatal(err)
  }
}
