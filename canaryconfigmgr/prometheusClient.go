package canaryconfigmgr

import (
	"fmt"
	"time"
	"golang.org/x/net/context"

	promApi "github.com/prometheus/client_golang/api/prometheus"
	"github.com/prometheus/common/model"
	log "github.com/sirupsen/logrus"
	"math"
)

type PrometheusApiClient struct {
	client promApi.QueryAPI
	// Add more stuff later
}

// TODO  prometheusSvc will need to come from helm chart value and passed to controller pod.
// controllerpod then passes this during canaryConfigMgr create
func makePrometheusClient(prometheusSvc string) *PrometheusApiClient {
	log.Printf("Making prom client with service : %s", prometheusSvc)
	promApiConfig := promApi.Config{
		Address: prometheusSvc,
	}

	promApiClient, err := promApi.New(promApiConfig)
	if err != nil {
		log.Errorf("Error creating prometheus api client for svc : %s, err : %v", prometheusSvc, err)
	}

	apiQueryClient := promApi.NewQueryAPI(promApiClient)

	log.Printf("Successfully made prom client")
	return &PrometheusApiClient{
		client: apiQueryClient,
	}
}

// TODO: Refine
func(promApi *PrometheusApiClient) GetFunctionFailurePercentage(funcName string, funcNs string, errorWindow time.Duration) float64{

	queryString := fmt.Sprintf("fission_function_calls_total{name=\"%s\",namespace=\"%s\"}", funcName, funcNs)
	//queryString := fmt.Sprintf("fission_function_errors_total")
	log.Printf("Making query towards prom api server, queryString : %s", queryString)
	val, err := promApi.client.Query(context.Background(), queryString, time.Now())
	if err != nil {
		log.Errorf("Error querying prometheus for fission_function_calls_total, err : %v", err)
	}

	log.Printf("Value retrieved from query : %v", val)

	functionCallTotal := extractValueFromQueryResult(val)

	queryString = fmt.Sprintf("fission_function_errors_total{name=\"%s\",namespace=\"%s\"}", funcName, funcNs)
	log.Printf("Making query towards prom api server, queryString : %s", queryString)
	val, err = promApi.client.Query(context.Background(), queryString, time.Now())
	if err != nil {
		log.Errorf("Error querying prometheus for fission_function_errors_total, err : %v", err)
	}

	log.Printf("Value retrieved from query : %v", val)

	functionErrNow := extractValueFromQueryResult(val)

	queryString = fmt.Sprintf("fission_function_errors_total{name=\"%s\",namespace=\"%s\"}", funcName, funcNs)
	log.Printf("Making query towards prom api server, queryString : %s", queryString)
	val, err = promApi.client.Query(context.Background(), queryString, time.Now().Add(-errorWindow))
	if err != nil {
		log.Errorf("Error querying prometheus for fission_function_errors_total for errorWindow, err : %v", err)
	}

	log.Printf("Value retrieved from query : %v", val)

	functionErrPrev := extractValueFromQueryResult(val)

	functionErrRate := math.Abs(functionErrPrev - functionErrNow)

	// This should give us the err percentage for this function from previous time window.
	return (functionErrRate / functionCallTotal) * 100

}

func extractValueFromQueryResult(val model.Value) float64 {
	switch {
	case val.Type() == model.ValScalar:
		scalarVal := val.(*model.Scalar)
		log.Printf("scalarValue : %v", scalarVal)
		log.Printf("Cannot be scalar. query should return vector")
		return 0

		// handle scalar stuff
	case val.Type() == model.ValVector:
		vectorVal := val.(model.Vector)
		total := float64(0)
		for _, elem := range vectorVal {
			log.Printf("labels : %v, Elem value : %v, timestamp : %v", elem.Metric, elem.Value, elem.Timestamp)
			total = total + float64(elem.Value)
		}
		return total

	default:
		log.Printf("type unrecognized")
		return 0
	}
}