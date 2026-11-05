package state

import (
 "context"
 "database/sql"
 "strconv"
 "strings"

 "github.com/braedonsaunders/ether/internal/core"
 "github.com/braedonsaunders/ether/internal/store"
)

func init(){store.Register("state_z_history_totals",1,`
CREATE TABLE state_play_totals(rating_key INTEGER NOT NULL,user_id INTEGER NOT NULL,plays INTEGER NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(rating_key,user_id));
CREATE INDEX state_play_totals_user ON state_play_totals(user_id,rating_key);
INSERT INTO state_play_totals SELECT rating_key,user_id,count(*),max(viewed_at) FROM state_history GROUP BY rating_key,user_id;
`)}
func historyAncestors(key core.RatingKey)[]core.RatingKey{
 keys:=[]core.RatingKey{key};if key.Kind()==core.KindEpisode{keys=append(keys,key.Parent(),key.Parent().Parent())};return keys
}
func addLifetimeTx(ctx context.Context,tx *sql.Tx,uid int64,key core.RatingKey,at int64)error{
 for _,k:=range historyAncestors(key){_,err:=tx.ExecContext(ctx,`INSERT INTO state_play_totals(rating_key,user_id,plays,last_at) VALUES(?,?,1,?) ON CONFLICT(rating_key,user_id) DO UPDATE SET plays=plays+1,last_at=max(last_at,excluded.last_at)`,int64(k),uid,at);if err!=nil{return err}};return nil
}
// HistoryTop counts event rows, independently of the current watched marker.
func(s *State)HistoryTop(ctx context.Context,q core.HistoryQuery,kind core.KeyKind)([]core.HistoryCount,error){
 where:=[]string{"1=1"};args:=[]any{}
 add:=func(c string,v any){where=append(where,c);args=append(args,v)}
 if q.UserID!=0{add("user_id=?",q.UserID)}
 if q.Since!=nil{add("viewed_at/1000000000>=?",*q.Since)};if q.Until!=nil{add("viewed_at/1000000000<=?",*q.Until)};if q.At!=nil{add("viewed_at/1000000000=?",*q.At)}
 const base int64=1_000_000_000_000_000
 expression:="rating_key"
 if kind==core.KindShow{expression="("+strconv.FormatInt(int64(core.KindShow)*base,10)+"+(rating_key%1000000000000000)/10000000)";add("rating_key>=?",int64(core.KindEpisode)*base);add("rating_key<?",int64(core.KindEpisode+1)*base)}else if kind==core.KindSeason{expression="("+strconv.FormatInt(int64(core.KindSeason)*base,10)+"+((rating_key%1000000000000000)/10000000)*10000+((rating_key%1000000000000000)/10000)%1000)";add("rating_key>=?",int64(core.KindEpisode)*base);add("rating_key<?",int64(core.KindEpisode+1)*base)}else{add("rating_key>=?",int64(kind)*base);add("rating_key<?",int64(kind+1)*base)}
 if q.Limit<=0{q.Limit=100};q.Limit=min(q.Limit,500)
 args=append(args,q.Limit)
 rows,err:=s.db.QueryContext(ctx,`SELECT `+expression+`,count(*),max(viewed_at),group_concat(DISTINCT user_id) FROM state_history WHERE `+strings.Join(where," AND ")+` GROUP BY 1 ORDER BY count(*) DESC,max(viewed_at) DESC,1 LIMIT ?`,args...)
 if err!=nil{return nil,err};defer rows.Close();var out []core.HistoryCount
 for rows.Next(){var h core.HistoryCount;var users string;if err:=rows.Scan(&h.Key,&h.Count,&h.LastAt,&users);err!=nil{return nil,err};for _,v:=range strings.Split(users,","){id,_:=strconv.ParseInt(v,10,64);h.Users=append(h.Users,id)};out=append(out,h)};return out,rows.Err()
}
func(s *State)LifetimeUsers(ctx context.Context,key core.RatingKey,q core.HistoryQuery)([]core.HistoryCount,error){
 where:="rating_key=?";args:=[]any{int64(key)}
 if q.Since!=nil{where+=" AND last_at/1000000000>=?";args=append(args,*q.Since)};if q.Until!=nil{where+=" AND last_at/1000000000<=?";args=append(args,*q.Until)};if q.At!=nil{where+=" AND last_at/1000000000=?";args=append(args,*q.At)}
 rows,err:=s.db.QueryContext(ctx,`SELECT user_id,plays,last_at FROM state_play_totals WHERE `+where+` ORDER BY plays DESC,last_at DESC,user_id LIMIT 500`,args...);if err!=nil{return nil,err};defer rows.Close();var out []core.HistoryCount
 for rows.Next(){var h core.HistoryCount;var uid int64;if err:=rows.Scan(&uid,&h.Count,&h.LastAt);err!=nil{return nil,err};h.Key=key;h.Users=[]int64{uid};out=append(out,h)};return out,rows.Err()
}
