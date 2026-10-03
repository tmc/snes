package server

import (
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "testing"
 "github.com/tmc/snes/internal/framecap"
 "github.com/tmc/snes/internal/recovery"
 "github.com/tmc/snes/internal/trace"
)

func TestDirectionLoaderRejectsUnauthenticated(t *testing.T) {
 for _,name:=range []string{"MissingManifestReceipt","MissingTraceReceipt","ForeignTraceRun","MalformedTraceJSON"} {
 t.Run(name,func(t *testing.T){
 dir:=t.TempDir()
 body:="{\"id\":0,\"schema\":2,\"kind\":\"run\",\"run\":{\"rom_sha256\":\"expected-rom\"}}\n{\"id\":1,\"schema\":2,\"kind\":\"ppu\",\"space\":\"oam\",\"op\":\"write\",\"addr\":543,\"value\":1,\"cycle\":50}\n"
 if name=="ForeignTraceRun" {body="{\"id\":0,\"schema\":2,\"kind\":\"run\",\"run\":{\"rom_sha256\":\"foreign-rom\"}}\n{\"id\":1,\"schema\":2,\"kind\":\"ppu\",\"space\":\"oam\",\"op\":\"write\",\"addr\":543,\"value\":1,\"cycle\":50}\n"}
 if name=="MalformedTraceJSON" {body+="NOT JSON\n"}
 path:=filepath.Join(dir,"trace.jsonl");if err:=os.WriteFile(path,[]byte(body),0600);err!=nil {t.Fatal(err)}
 fc:=&framecap.Capture{Dir:dir,Header:framecap.Header{Run:&trace.RunInfo{ROMSHA256:"expected-rom"},Trace:path},Records:[]framecap.Record{{Number:1,Start:100,VBlank:200}}}
 fc.Header.Kind="frame_run";fc.Header.Schema=1;fc.Records[0].Kind="frame"
 header,_:=json.Marshal(fc.Header);record,_:=json.Marshal(fc.Records[0]);manifest:=append(append(append(header,'\n'),record...), '\n')
 if err:=os.WriteFile(filepath.Join(dir,framecap.ManifestName),manifest,0600);err!=nil {t.Fatal(err)}
 if name!="MissingManifestReceipt" {fc.Receipt=&framecap.Receipt{Outcome:trace.OutcomeComplete,ManifestSHA256:fmt.Sprintf("%x",sha256.Sum256(manifest))}}
 if name!="MissingTraceReceipt" {
 receipt,_:=json.Marshal(trace.Receipt{Schema:2,Outcome:trace.OutcomeComplete,EventCount:2,StreamSHA256:fmt.Sprintf("%x",sha256.Sum256([]byte(body)))})
 for _,rp:=range []string{"receipt.json","trace.receipt.json","trace.jsonl.receipt.json"} {if err:=os.WriteFile(filepath.Join(dir,rp),receipt,0600);err!=nil {t.Fatal(err)}}
 }
 doc:=&recovery.Document{};doc.ROM.NormalizedSHA256="expected-rom"
 eng,_,err:=loadProjectProvenance(dir,doc,nil,fc)
 if err==nil {t.Fatalf("invalid producer bundle published engine=%v HasFrame1=%v",eng!=nil,eng.HasFrame(1))}
 })
 }
}
