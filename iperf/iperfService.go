package iperf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	"strconv"
	"strings"
	"testk8s/utils"
)

var nameService = "my-service-iperf"
var namespaceUDP = "testiperfudp"

func TCPservice(clientset *kubernetes.Clientset, casus int, multiple bool) string {

	node = utils.SetNodeSelector(casus)
	nsCR := utils.CreateNS(clientset, namespace)
	fmt.Printf("Namespace %s created \n", nsCR.Name)

	if multiple {
		fmt.Println("the program will create multiple service and endpoints")
		utils.CreateBulk(10, clientset, namespace)
	}

	svc := apiv1.Service{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameService,
			Namespace: namespace,
			//Labels: map[string]string{"":""},
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{
				Name:       "tcpport",
				Protocol:   "TCP",
				Port:       5001,
				TargetPort: intstr.IntOrString{intstr.Type(0), 5001, "5001"},
			}},
			Selector: map[string]string{"app": "iperfserver"},
		},
	}
	svcCr, errCr := clientset.CoreV1().Services(namespace).Create(context.TODO(), &svc, metav1.CreateOptions{})
	if errCr != nil {
		panic(errCr)
	}
	fmt.Println("Service my-service-iperf created " + svcCr.GetName())

	var netSpeeds [12]float64

	for i := 0; i < 12; i++ {
		dep := createIperfDeployment("5001", namespace)
		fmt.Println("Creating deployment...")
		res, errDepl := clientset.AppsV1().Deployments(namespace).Create(context.TODO(), dep, metav1.CreateOptions{})
		if errDepl != nil {
			panic(errDepl)
		}
		fmt.Printf("Created deployment %q.\n", res.GetObjectMeta().GetName())
		deps, errD := clientset.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{})
		if errD != nil {
			panic(errD.Error())
		}

		podvect, errP := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
		if errP != nil {
			panic(errP)
		}
		fmt.Print("Wait for pod creation..")
		for {
			podvect, errP = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
			if errP != nil {
				panic(errP)
			}
			var num int = len(podvect.Items)
			if num != 0 {
				fmt.Printf("\n")
				break
			}
			fmt.Print(".")
		}
		fmt.Printf("There are %d pods and %d depl in the cluster\n", len(podvect.Items), len(deps.Items))
		pod := podvect.Items[0]
		ctl := 0
		for ctl != 1 {
			switch pod.Status.Phase {
			case apiv1.PodRunning:
				{
					ctl = 1
					break
				}
			case apiv1.PodPending:
				{
					podvect, errP = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
					if errP != nil {
						panic(errP)
					}
					pod = podvect.Items[0]
				}
			case apiv1.PodFailed, apiv1.PodSucceeded:
				panic("error in pod creation")
			}
		}
		serviceC, errSvcSearch := clientset.CoreV1().Services(namespace).Get(context.TODO(), nameService, metav1.GetOptions{})
		if errors.IsNotFound(errSvcSearch) {
			fmt.Printf("svc %s in namespace %s not found\n", nameService, namespace)
		} else if statusError, isStatus := errSvcSearch.(*errors.StatusError); isStatus {
			fmt.Printf("Error getting svc %s in namespace %s: %v\n",
				nameService, namespace, statusError.ErrStatus.Message)
		} else if errSvcSearch != nil {
			panic(errSvcSearch.Error())
		} else {
			fmt.Printf("Found svc %s in namespace %s\n", svc.Name, namespace)
			svcIP := serviceC.Spec.ClusterIP
			fmt.Printf("Service IP: %s\n", svcIP)
		}

		command := "iperf3 -c " + serviceC.Spec.ClusterIP + " -p 5001 -V -N -t 10 -Z > file.txt; cat file.txt"
		fmt.Println("Creating Iperf Client: " + command)
		jobsClient := clientset.BatchV1().Jobs(namespace)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: namespace,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit: pointer.Int32Ptr(4),
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "iperfclient",
						Labels: map[string]string{"app": "iperfclient"},
					},
					Spec: apiv1.PodSpec{
						Containers: []apiv1.Container{
							{
								Name:    "iperfclient",
								Image:   "networkstatic/iperf3",
								Command: []string{"/bin/bash"},
								Args:    []string{"-c", command},
							},
						},
						RestartPolicy: "OnFailure",
						NodeSelector:  map[string]string{"type": node},
					},
				},
			},
		}
		result1, errJ := jobsClient.Create(context.TODO(), job, metav1.CreateOptions{})
		if errJ != nil {
			fmt.Println(errJ.Error())
			panic(errJ)
		}
		fmt.Printf("Created job %q.\n", result1.GetObjectMeta().GetName())
		podClient, errC := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
		if errC != nil {
			panic(errC)
		}
		for {
			if len(podClient.Items) != 0 {
				break
			}
			podClient, errC = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
			if errC != nil {
				panic(errC)
			}
		}
		fmt.Printf("Created pod %q.\n", podClient.Items[0].Name)
		pod = podClient.Items[0]
		var str string
		ctl = 0
		for ctl != 1 {
			switch pod.Status.Phase {
			case apiv1.PodRunning, apiv1.PodPending:
				{
					podClient, errC = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
					if errC != nil {
						panic(errC)
					}
					pod = podClient.Items[0]
				}
			case apiv1.PodSucceeded:
				{
					logs := clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &apiv1.PodLogOptions{})
					podLogs, errLogs := logs.Stream(context.TODO())
					if errLogs != nil {
						panic(errLogs)
					}
					defer podLogs.Close()
					buf := new(bytes.Buffer)
					_, errBuf := io.Copy(buf, podLogs)
					if errBuf != nil {
						panic(errBuf)
					}
					str = buf.String()
					ctl = 1
					break
				}
			case apiv1.PodFailed:
				panic("error panic in pod created by job")
			}
		}

		//works on strings
		fmt.Println(str)
		if strings.Contains(str, "Connection refused") {
			utils.DeleteNS(clientset, namespace)
			panic("error in client server communication")
		} else {
			vectString := strings.Split(str, "0.00-10.00 ")
			substringSpeed := strings.Split(vectString[1], "  ")
			speed := strings.Split(substringSpeed[2], " ")
			velspeed, errConv := strconv.ParseFloat(speed[0], 32)
			if errConv != nil {
				panic(errConv)
			}

			switch speed[1] {
			case "Mbits/sec":
				velspeed = velspeed / 1000
			case "Kbits/sec":
				velspeed = velspeed / 1000000
			case "Gbits/sec":
				fmt.Println("Ok, Gbits/sec")
			}
			fmt.Printf("%d %f %s \n", i, velspeed, speed[1])
			//todo vedere cosa succede con float 32, per ora 64
			netSpeeds[i] = velspeed
		}

		//Deployment delete

		errDplDel := clientset.AppsV1().Deployments(namespace).Delete(context.TODO(), deplName, metav1.DeleteOptions{})
		if errDplDel != nil {
			panic(errDplDel)
		}
		DeplSize, errWaitDeplDel := clientset.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
		if errWaitDeplDel != nil {
			panic(errWaitDeplDel)
		}
		for len(DeplSize.Items) != 0 {
			DeplSize, errWaitDeplDel = clientset.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
			if errWaitDeplDel != nil {
				panic(errWaitDeplDel)
			}
		}

		PodDeplSize, errWaitPodDeplDel := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
		if errWaitPodDeplDel != nil {
			panic(errWaitPodDeplDel)
		}
		for len(PodDeplSize.Items) != 0 {
			PodDeplSize, errWaitPodDeplDel = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfserver"})
			if errWaitPodDeplDel != nil {
				panic(errWaitPodDeplDel)
			}
		}
		//Job delete

		errJobDel := clientset.BatchV1().Jobs(namespace).Delete(context.TODO(), jobName, metav1.DeleteOptions{})
		if errJobDel != nil {
			panic(errJobDel)
		}

		JobSize, errWaitJobDel := clientset.BatchV1().Jobs(namespace).List(context.TODO(), metav1.ListOptions{})
		if errWaitJobDel != nil {
			panic(errWaitJobDel)
		}
		for len(JobSize.Items) != 0 {
			JobSize, errWaitJobDel = clientset.BatchV1().Jobs(namespace).List(context.TODO(), metav1.ListOptions{})
			if errWaitJobDel != nil {
				panic(errWaitJobDel)
			}
		}

		//Pod delete
		errPodDel := clientset.CoreV1().Pods(namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})

		if errPodDel != nil {
			panic(errPodDel)
		}

		PodSize, errWaitPodDel := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
		if errWaitPodDel != nil {
			panic(errWaitPodDel)
		}
		for len(PodSize.Items) != 0 {
			PodSize, errWaitPodDel = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
			if errWaitPodDel != nil {
				panic(errWaitPodDel)
			}
		}

	}

	utils.DeleteBulk(10, clientset, namespace)
	utils.DeleteNS(clientset, namespace)
	fmt.Printf("Namespace %s deleted \n", namespace)
	return fmt.Sprintf("%f", utils.AvgSpeed(netSpeeds)) + " Gbits/sec"
}

func UDPservice(clientset *kubernetes.Clientset, casus int, multiple bool) string {

	node = utils.SetNodeSelector(casus)
	nsCR := utils.CreateNS(clientset, namespaceUDP)
	fmt.Printf("Namespace %s created \n", nsCR.GetName())

	svc := apiv1.Service{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameService,
			Namespace: namespaceUDP,
			//Labels: map[string]string{"":""},
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{
				Name:       "udpport",
				Protocol:   "UDP",
				Port:       15201,
				TargetPort: intstr.IntOrString{intstr.Type(0), 15201, "15201"},
			}},
			Selector: map[string]string{"app": "iperfserver"},
		},
	}
	svcCr, errCr := clientset.CoreV1().Services(namespaceUDP).Create(context.TODO(), &svc, metav1.CreateOptions{})
	if errCr != nil {
		panic(errCr)
	}
	fmt.Println("Service UDP my-service-iperf created " + svcCr.GetName())

	var netSpeeds [12]float64

	for i := 0; i < 12; i++ {
		dep := createIperfDeployment("15201", namespaceUDP)
		fmt.Println("Creating deployment...")
		res, errDepl := clientset.AppsV1().Deployments(namespaceUDP).Create(context.TODO(), dep, metav1.CreateOptions{})
		if errDepl != nil {
			panic(errDepl)
		}
		fmt.Printf("Created deployment %q.\n", res.GetObjectMeta().GetName())
		deps, errD := clientset.AppsV1().Deployments(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
		if errD != nil {
			panic(errD.Error())
		}

		podvect, errP := clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
		if errP != nil {
			panic(errP)
		}
		fmt.Print("Wait for pod creation..")
		for {
			podvect, errP = clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
			if errP != nil {
				panic(errP)
			}
			var num int = len(podvect.Items)
			if num != 0 {
				fmt.Printf("\n")
				break
			}
			fmt.Print(".")
		}
		fmt.Printf("There are %d pods and %d depl in the cluster\n", len(podvect.Items), len(deps.Items))
		pod := podvect.Items[0]
		ctl := 0
		for ctl != 1 {
			switch pod.Status.Phase {
			case apiv1.PodRunning:
				{
					ctl = 1
					break
				}
			case apiv1.PodPending:
				{
					podvect, errP = clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
					if errP != nil {
						panic(errP)
					}
					pod = podvect.Items[0]
				}
			case apiv1.PodFailed, apiv1.PodSucceeded:
				panic("error in pod creation")
			}
		}
		serviceC, errSvcSearch := clientset.CoreV1().Services(namespaceUDP).Get(context.TODO(), nameService, metav1.GetOptions{})
		if errors.IsNotFound(errSvcSearch) {
			fmt.Printf("svc %s in namespace %s not found\n", nameService, namespaceUDP)
		} else if statusError, isStatus := errSvcSearch.(*errors.StatusError); isStatus {
			fmt.Printf("Error getting svc %s in namespace %s: %v\n",
				nameService, namespaceUDP, statusError.ErrStatus.Message)
		} else if errSvcSearch != nil {
			panic(errSvcSearch.Error())
		} else {
			fmt.Printf("Found svc %s in namespace %s\n", svc.Name, namespaceUDP)
			svcIP := serviceC.Spec.ClusterIP
			fmt.Printf("Service IP: %s\n", svcIP)
		}

		command := "iperf3 -c " + serviceC.Spec.ClusterIP + " -u -b 0 -p 15201 -V -N -t 10 -Z > file.txt; cat file.txt"
		fmt.Println("Creating UDP Iperf Client: " + command)
		jobsClient := clientset.BatchV1().Jobs(namespaceUDP)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: namespaceUDP,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit: pointer.Int32Ptr(4),
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "iperfclient",
						Labels: map[string]string{"app": "iperfclient"},
					},
					Spec: apiv1.PodSpec{
						Containers: []apiv1.Container{
							{
								Name:    "iperfclient",
								Image:   "networkstatic/iperf3",
								Command: []string{"/bin/bash"},
								Args:    []string{"-c", command},
							},
						},
						RestartPolicy: "OnFailure",
						NodeSelector:  map[string]string{"type": node},
					},
				},
			},
		}
		result1, errJ := jobsClient.Create(context.TODO(), job, metav1.CreateOptions{})
		if errJ != nil {
			fmt.Println(errJ.Error())
			panic(errJ)
		}
		fmt.Printf("Created job %q.\n", result1.GetObjectMeta().GetName())
		podClient, errC := clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
		if errC != nil {
			panic(errC)
		}
		for {
			if len(podClient.Items) != 0 {
				break
			}
			podClient, errC = clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
			if errC != nil {
				panic(errC)
			}
		}
		fmt.Printf("Created pod %q.\n", podClient.Items[0].Name)
		pod = podClient.Items[0]
		var str string
		ctl = 0
		for ctl != 1 {
			switch pod.Status.Phase {
			case apiv1.PodRunning, apiv1.PodPending:
				{
					podClient, errC = clientset.CoreV1().Pods(namespaceUDP).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=iperfclient"})
					if errC != nil {
						panic(errC)
					}
					pod = podClient.Items[0]
				}
			case apiv1.PodSucceeded:
				{
					logs := clientset.CoreV1().Pods(namespaceUDP).GetLogs(pod.Name, &apiv1.PodLogOptions{})
					podLogs, errLogs := logs.Stream(context.TODO())
					if errLogs != nil {
						panic(errLogs)
					}
					defer podLogs.Close()
					buf := new(bytes.Buffer)
					_, errBuf := io.Copy(buf, podLogs)
					if errBuf != nil {
						panic(errBuf)
					}
					str = buf.String()
					fmt.Println(str)
					ctl = 1
					break
				}
			case apiv1.PodFailed:
				panic("error panic in pod created by job")
			}
		}

		//works on strings
		if strings.Contains(str, "Connection refused") || strings.Contains(str, "Connection timed out") {
			utils.DeleteNS(clientset, namespaceUDP)
			panic("error in client server communication")
		} else {
			vectString := strings.Split(str, "0.00-10.00 ")
			substringSpeed := strings.Split(vectString[1], "  ")
			speed := strings.Split(substringSpeed[2], " ")
			velspeed, errConv := strconv.ParseFloat(speed[0], 32)
			if errConv != nil {
				panic(errConv)
			}

			switch speed[1] {
			case "Mbits/sec":
				velspeed = velspeed / 1000
			case "Kbits/sec":
				velspeed = velspeed / 1000000
			case "Gbits/sec":
				fmt.Println("Ok, Gbits/sec")
			}
			fmt.Printf("%d %f %s \n", i, velspeed, speed[1])
			//todo vedere cosa succede con float 32, per ora 64
			netSpeeds[i] = velspeed
		}
		//Deployment delete

		errDplDel := clientset.AppsV1().Deployments(namespaceUDP).Delete(context.TODO(), deplName, metav1.DeleteOptions{})
		if errDplDel != nil {
			panic(errDplDel)
		}
		DeplSize, errWaitDeplDel := clientset.AppsV1().Deployments(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
		if errWaitDeplDel != nil {
			panic(errWaitDeplDel)
		}
		for len(DeplSize.Items) != 0 {
			DeplSize, errWaitDeplDel = clientset.AppsV1().Deployments(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
			if errWaitDeplDel != nil {
				panic(errWaitDeplDel)
			}
		}

		//Job delete

		errJobDel := clientset.BatchV1().Jobs(namespaceUDP).Delete(context.TODO(), jobName, metav1.DeleteOptions{})
		if errJobDel != nil {
			panic(errJobDel)
		}

		JobSize, errWaitJobDel := clientset.BatchV1().Jobs(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
		if errWaitJobDel != nil {
			panic(errWaitJobDel)
		}
		for len(JobSize.Items) != 0 {
			JobSize, errWaitJobDel = clientset.BatchV1().Jobs(namespaceUDP).List(context.TODO(), metav1.ListOptions{})
			if errWaitJobDel != nil {
				panic(errWaitJobDel)
			}
		}

		//Pod delete
		errPodDel := clientset.CoreV1().Pods(namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})

		if errPodDel != nil {
			panic(errPodDel)
		}

		PodSize, errWaitPodDel := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
		if errWaitPodDel != nil {
			panic(errWaitPodDel)
		}
		for len(PodSize.Items) != 0 {
			PodSize, errWaitPodDel = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
			if errWaitPodDel != nil {
				panic(errWaitPodDel)
			}
		}

	}

	utils.DeleteNS(clientset, namespace)
	fmt.Printf("Namespace %s deleted \n", namespace)
	return fmt.Sprintf("%f", utils.AvgSpeed(netSpeeds)) + " Gbits/sec"
}