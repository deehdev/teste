/ === FILE: storage.go ===
package main


import (
"encoding/json"
"log"
"os"
"path/filepath"
)


func saveJSONFile(path string, v interface{}) error {
dir := filepath.Dir(path)
if err := os.MkdirAll(dir, 0o755); err != nil {
return err
}
tmp := path + ".tmp"
f, err := os.Create(tmp)
if err != nil {
return err
}
enc := json.NewEncoder(f)
enc.SetIndent("", " ")
if err := enc.Encode(v); err != nil {
f.Close()
return err
}
f.Sync()
f.Close()
return os.Rename(tmp, path)
}


func loadJSONFile(path string, dest interface{}) error {
f, err := os.Open(path)
if err != nil {
return err
}
defer f.Close()
dec := json.NewDecoder(f)
return dec.Decode(dest)
}


func persistAll() {
if err := saveJSONFile(usersFile, users); err != nil {
log.Println("persist users erro:", err)
}
if err := saveJSONFile(channelsFile, channels); err != nil {
log.Println("persist channels erro:", err)
}
if err := saveJSONFile(subsFile, subscriptions); err != nil {
log.Println("persist subs erro:", err)
}
if err := saveJSONFile(logsFile, logs); err != nil {
log.Println("persist logs erro:", err)
}
}