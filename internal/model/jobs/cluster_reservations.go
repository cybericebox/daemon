package jobsModel

// ClusterReservationsArgs drives the periodic reconcile of the cluster reservations: every live calendar reservation
// is split again and every agent is brought in line with the parts it should hold (orphans deleted, unsent parts
// retried). Changes are pushed at once by the calendar; this pass is the safety net.
type ClusterReservationsArgs struct{}

func (ClusterReservationsArgs) Kind() string { return "cluster_reservations_reconcile" }
