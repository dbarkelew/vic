*** Settings ***
Documentation  Test 8-1 - Verify VM guest tools integration
Resource  ../../resources/Util.robot
Suite Setup  Install VIC Appliance To Test Server
Suite Teardown  Cleanup VIC Appliance On Test Server

*** Test Cases ***
Verify VCH VM guest IP is reported
    ${ip}=  Run  govc vm.ip ${vch-name}
    # VCH ip should be the same as docker host param
    Should Contain  ${params}  ${ip}

Verify container VM guest IP is reported
    ${rc}  ${output}=  Run And Return Rc And Output  docker ${params} pull busybox
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    ${rc}  ${id}=  Run And Return Rc And Output  docker ${params} run -d busybox /bin/top
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${id}  Error
    Run  govc vm.ip ${id}

Stop container VM using guest shutdown
    ${rc}=  Run And Return Rc  docker ${params} pull busybox
    Should Be Equal As Integers  ${rc}  0
    ${rc}  ${id}=  Run And Return Rc And Output  docker ${params} run -d busybox /bin/top
    Should Be Equal As Integers  ${rc}  0
    Run  govc vm.ip ${id}
    ${rc}=  Run And Return Rc  govc vm.power -s ${id}
    Should Be Equal As Integers  ${rc}  0

Signal container VM using vix command
    ${rc}=  Run And Return Rc  docker ${params} pull busybox
    Should Be Equal As Integers  ${rc}  0
    ${rc}  ${id}=  Run And Return Rc And Output  docker ${params} run -d busybox /bin/top
    Should Be Equal As Integers  ${rc}  0
    Run  govc vm.ip ${id}
    # Invalid command
    ${rc}=  Run And Return Rc  govc guest.start -vm ${id} -l ${id} hello world
    Should Be Equal As Integers  ${rc}  1
    # Invalid id (via auth user)
    ${rc}=  Run And Return Rc  govc guest.start -vm ${id} kill USR1
    Should Be Equal As Integers  ${rc}  1
    # OK
    ${rc}=  Run And Return Rc  govc guest.start -vm ${id} -l ${id} kill USR1
    Should Be Equal As Integers  ${rc}  0