package eventself

import (
 "encoding/json"
 "os"
 "testing"
)

func TestGeneratedSwaggerMatchesFrozenLabPascalCaseAndNullability(t *testing.T){
 raw,err:=os.ReadFile("../apidocs/swagger.json");if err!=nil{t.Fatal(err)}
 var doc struct{Definitions map[string]struct{Properties map[string]map[string]any}}
 if err=json.Unmarshal(raw,&doc);err!=nil{t.Fatal(err)}
 p:=doc.Definitions["labview.ParticipantLabResponse"].Properties
 for _,key:=range []string{"ID","EventExerciseID","Revision","LogicalClosed","CloseReason","ClosedAt","RuntimeState","CanStop","CanRestart","SnapshotPolicy","RetentionUntil"}{if p[key]==nil{t.Fatalf("missing PascalCase %s in %v",key,p)}}
 for _,key:=range []string{"CloseReason","ClosedAt","RetentionUntil"}{if p[key]["x-nullable"]!=true{t.Fatalf("missing nullable %s: %v",key,p[key])}}
 if p["Revision"]["type"]!="string"{t.Fatal(p["Revision"])}
 for _,key:=range []string{"CPUMillicores","MemoryBytes"}{if doc.Definitions["event.ComputeView"].Properties[key]["type"]!="string"{t.Fatalf("quantity %s",key)}}
}
